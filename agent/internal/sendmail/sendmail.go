// Package sendmail is the sendmail command seen by commands NimVor runs as a
// website account (deploy hooks, cron "run now", app installers).
//
// Those commands run in transient units with NoNewPrivileges=yes, where
// Postfix's own sendmail cannot work: it hands the message to setgid
// postdrop, which cannot gain its group and retries forever. The agent
// binary is bind-mounted over sendmail inside those units only and, when
// started under that name, submits the message over SMTP to the mail
// server on this machine instead. It needs no privilege: any process may
// connect to 127.0.0.1:25, and Postfix applies its usual checks.
package sendmail

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"os"
	"os/user"
	"strings"
	"time"
)

// Exit statuses (sysexits.h), as sendmail programs return them.
const (
	exOK          = 0
	exUsage       = 64
	exDataErr     = 65
	exUnavailable = 69
	exSoftware    = 70
	exTempFail    = 75
)

// Server is the SMTP server messages are submitted to (tests change it).
var Server = "127.0.0.1:25"

// maxMessage bounds the message read from standard input.
const maxMessage = 64 << 20

// timeout bounds the whole SMTP conversation.
const timeout = 60 * time.Second

type options struct {
	fromHeaders bool   // -t
	ignoreDots  bool   // -i, -oi
	sender      string // -f, -r
	senderSet   bool
	fullName    string // -F
	recipients  []string
}

// Main runs the sendmail command with its arguments (without the program
// name) and returns the exit status.
func Main(args []string, stdin io.Reader, stderr io.Writer) int {
	fail := func(code int, format string, a ...any) int {
		fmt.Fprintf(stderr, "sendmail (NimVor): "+format+"\n", a...)
		return code
	}
	o, err := parseArgs(args)
	if err != nil {
		return fail(exUsage, "%v", err)
	}
	msg, err := readMessage(stdin, o.ignoreDots)
	if err != nil {
		return fail(exDataErr, "%v", err)
	}
	header, body, err := splitMessage(msg)
	if err != nil {
		return fail(exDataErr, "%v", err)
	}
	rcpts := append([]string{}, o.recipients...)
	if o.fromHeaders {
		for _, name := range []string{"To", "Cc", "Bcc"} {
			for _, v := range header.values(name) {
				list, err := mail.ParseAddressList(v)
				if err != nil {
					return fail(exDataErr, "invalid %s header: %v", name, err)
				}
				for _, a := range list {
					rcpts = append(rcpts, a.Address)
				}
			}
		}
		header.remove("Bcc")
	}
	if len(rcpts) == 0 {
		return fail(exUsage, "no recipients")
	}
	for _, r := range append(rcpts, o.sender) {
		if strings.ContainsAny(r, "\r\n<>") {
			return fail(exDataErr, "invalid address %q", r)
		}
	}
	sender := o.sender
	if !o.senderSet {
		sender = defaultSender()
	}
	if !header.has("From") {
		from := sender
		if o.fullName != "" {
			from = (&mail.Address{Name: o.fullName, Address: sender}).String()
		}
		header.add("From", from)
	}
	if !header.has("Date") {
		header.add("Date", time.Now().Format(time.RFC1123Z))
	}
	var data bytes.Buffer
	header.writeTo(&data)
	data.WriteString("\n")
	data.Write(body)

	code, err := submit(sender, rcpts, data.Bytes())
	if err != nil {
		return fail(code, "%v", err)
	}
	return exOK
}

func parseArgs(args []string) (options, error) {
	var o options
	value := func(i *int, a, flag string) (string, error) {
		if len(a) > len(flag) {
			return a[len(flag):], nil
		}
		*i++
		if *i >= len(args) {
			return "", fmt.Errorf("%s needs a value", flag)
		}
		return args[*i], nil
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			o.recipients = append(o.recipients, args[i+1:]...)
			return o, nil
		case !strings.HasPrefix(a, "-") || a == "-":
			o.recipients = append(o.recipients, a)
		case a == "-t":
			o.fromHeaders = true
		case a == "-i" || a == "-oi":
			o.ignoreDots = true
		case strings.HasPrefix(a, "-f"), strings.HasPrefix(a, "-r"):
			v, err := value(&i, a, a[:2])
			if err != nil {
				return o, err
			}
			o.sender, o.senderSet = strings.Trim(v, "<>"), true
		case strings.HasPrefix(a, "-F"):
			v, err := value(&i, a, "-F")
			if err != nil {
				return o, err
			}
			o.fullName = v
		case strings.HasPrefix(a, "-B"), strings.HasPrefix(a, "-N"), strings.HasPrefix(a, "-R"), strings.HasPrefix(a, "-V"),
			strings.HasPrefix(a, "-C"), strings.HasPrefix(a, "-L"):
			// Options with a value that do not apply here.
			if _, err := value(&i, a, a[:2]); err != nil {
				return o, err
			}
		case a == "-bm" || strings.HasPrefix(a, "-o") || strings.HasPrefix(a, "-d") || a == "-n" || a == "-U" || a == "-v" || a == "-m":
			// Default mode, delivery options, verbosity: nothing to do.
		case strings.HasPrefix(a, "-b"):
			return o, fmt.Errorf("mode %s is not supported: only sending a message is", a)
		default:
			return o, fmt.Errorf("unknown option %s", a)
		}
	}
	return o, nil
}

// readMessage reads the message; without -i a line with a single "." ends
// it, as with sendmail.
func readMessage(r io.Reader, ignoreDots bool) ([]byte, error) {
	var out bytes.Buffer
	br := bufio.NewReader(io.LimitReader(r, maxMessage+1))
	for {
		line, err := br.ReadString('\n')
		if !ignoreDots && strings.TrimRight(line, "\r\n") == "." && strings.HasSuffix(line, "\n") {
			break
		}
		out.WriteString(strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"))
		if line != "" {
			out.WriteString("\n")
		}
		if out.Len() > maxMessage {
			return nil, errors.New("message too large")
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return out.Bytes(), nil
}

// header keeps the header lines in order, each with its continuation lines.
type header []string

func (h header) name(i int) string {
	n, _, _ := strings.Cut(h[i], ":")
	return strings.TrimSpace(n)
}

func (h header) values(name string) []string {
	var v []string
	for i := range h {
		if strings.EqualFold(h.name(i), name) {
			_, val, _ := strings.Cut(h[i], ":")
			v = append(v, strings.TrimSpace(strings.ReplaceAll(val, "\n", "")))
		}
	}
	return v
}

func (h header) has(name string) bool { return len(h.values(name)) > 0 }

func (h *header) remove(name string) {
	out := (*h)[:0]
	for i := range *h {
		if !strings.EqualFold(h.name(i), name) {
			out = append(out, (*h)[i])
		}
	}
	*h = out
}

func (h *header) add(name, value string) { *h = append(*h, name+": "+value) }

func (h header) writeTo(b *bytes.Buffer) {
	for _, l := range h {
		b.WriteString(l)
		b.WriteString("\n")
	}
}

// splitMessage separates the header (up to the first empty line) from the
// body.
func splitMessage(msg []byte) (header, []byte, error) {
	var h header
	rest := msg
	for len(rest) > 0 {
		line, after, found := bytes.Cut(rest, []byte("\n"))
		if len(line) == 0 {
			return h, after, nil
		}
		if line[0] == ' ' || line[0] == '\t' {
			if len(h) == 0 {
				return nil, nil, errors.New("the message starts with a continuation line")
			}
			h[len(h)-1] += "\n" + string(line)
		} else if bytes.IndexByte(line, ':') > 0 {
			h = append(h, string(line))
		} else {
			// No header block: all of it is the body.
			return h, rest, nil
		}
		if !found {
			return h, nil, nil
		}
		rest = after
	}
	return h, nil, nil
}

func defaultSender() string {
	name := "nobody"
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "localhost"
	}
	return name + "@" + host
}

// submit sends the message; the returned status tells a temporary failure
// (the caller may retry) from a refusal.
func submit(sender string, rcpts []string, data []byte) (int, error) {
	conn, err := net.DialTimeout("tcp", Server, 10*time.Second)
	if err != nil {
		return exTempFail, fmt.Errorf("the mail server on %s cannot be reached: %v", Server, err)
	}
	conn.SetDeadline(time.Now().Add(timeout))
	c, err := smtp.NewClient(conn, "localhost")
	if err != nil {
		conn.Close()
		return status(err), fmt.Errorf("mail server: %v", err)
	}
	defer c.Close()
	if err := c.Hello("localhost"); err != nil {
		return status(err), fmt.Errorf("EHLO: %v", err)
	}
	if err := c.Mail(sender); err != nil {
		return status(err), fmt.Errorf("sender %s refused: %v", sender, err)
	}
	for _, r := range rcpts {
		if err := c.Rcpt(r); err != nil {
			return status(err), fmt.Errorf("recipient %s refused: %v", r, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return status(err), fmt.Errorf("DATA: %v", err)
	}
	if _, err := w.Write(data); err != nil {
		return exTempFail, err
	}
	if err := w.Close(); err != nil {
		return status(err), fmt.Errorf("message refused: %v", err)
	}
	_ = c.Quit()
	return exOK, nil
}

func status(err error) int {
	var te *textproto.Error
	if errors.As(err, &te) {
		if te.Code >= 500 {
			return exUnavailable
		}
		return exTempFail
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return exTempFail
	}
	return exSoftware
}
