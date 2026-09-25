package sendmail

import (
	"bufio"
	"bytes"
	"net"
	"strings"
	"sync"
	"testing"
)

// fakeServer is a minimal SMTP server recording one conversation. Mail to
// a recipient starting with "refused" gets a 550.
type fakeServer struct {
	mu     sync.Mutex
	sender string
	rcpts  []string
	data   string
}

func startServer(t *testing.T) *fakeServer {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	old := Server
	Server = l.Addr().String()
	t.Cleanup(func() { Server = old })
	s := &fakeServer{}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

func (s *fakeServer) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	say := func(l string) { c.Write([]byte(l + "\r\n")) }
	say("220 fake")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		cmd := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			say("250 fake")
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			s.mu.Lock()
			s.sender = strings.Trim(line[len("MAIL FROM:"):], "<>")
			s.mu.Unlock()
			say("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			rcpt := strings.Trim(line[len("RCPT TO:"):], "<>")
			if strings.HasPrefix(rcpt, "refused") {
				say("550 no such user")
				continue
			}
			s.mu.Lock()
			s.rcpts = append(s.rcpts, rcpt)
			s.mu.Unlock()
			say("250 ok")
		case cmd == "DATA":
			say("354 go")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(strings.TrimPrefix(l, "."))
			}
			s.mu.Lock()
			s.data = b.String()
			s.mu.Unlock()
			say("250 queued")
		case cmd == "QUIT":
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}

func (s *fakeServer) got() (string, []string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sender, s.rcpts, s.data
}

// PHP's mail() runs "sendmail -t -i": recipients come from the headers,
// Bcc is removed, a lone "." does not end the message.
func TestSendsLikePHPMail(t *testing.T) {
	s := startServer(t)
	msg := "To: Alice <alice@example.test>, bob@example.test\nCc: carol@example.test\nBcc: hidden@example.test\n  , hidden2@example.test\nSubject: hi\n\nline 1\n.\n.dotted\nlast"
	var stderr bytes.Buffer
	if code := Main([]string{"-t", "-i", "-f", "site@example.test"}, strings.NewReader(msg), &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	sender, rcpts, data := s.got()
	if sender != "site@example.test" {
		t.Errorf("sender = %q", sender)
	}
	if strings.Join(rcpts, ",") != "alice@example.test,bob@example.test,carol@example.test,hidden@example.test,hidden2@example.test" {
		t.Errorf("recipients = %v", rcpts)
	}
	for _, want := range []string{"Subject: hi\r\n", "From: site@example.test\r\n", "Date: ", "\r\n\r\nline 1\r\n.\r\n.dotted\r\nlast\r\n"} {
		if !strings.Contains(data, want) {
			t.Errorf("message lacks %q:\n%s", want, data)
		}
	}
	if strings.Contains(data, "hidden") || strings.Contains(data, "Bcc") {
		t.Errorf("Bcc was sent:\n%s", data)
	}
}

// Without -i a line with a single "." ends the message; recipients on the
// command line; -F names the sender; an existing From header is kept.
func TestCommandLineRecipientsAndDots(t *testing.T) {
	s := startServer(t)
	var stderr bytes.Buffer
	code := Main([]string{"-oi", "-Fsite owner", "-fowner@example.test", "--", "dave@example.test"}, strings.NewReader("Subject: a\n\nbody\n"), &stderr)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if _, rcpts, data := s.got(); len(rcpts) != 1 || rcpts[0] != "dave@example.test" || !strings.Contains(data, `From: "site owner" <owner@example.test>`) {
		t.Errorf("recipients %v, message:\n%s", rcpts, data)
	}

	s = startServer(t)
	Main([]string{"erin@example.test"}, strings.NewReader("From: me@example.test\nSubject: b\n\nkept\n.\ndropped\n"), &stderr)
	if _, _, data := s.got(); !strings.Contains(data, "kept") || strings.Contains(data, "dropped") || strings.Count(data, "From:") != 1 {
		t.Errorf("message:\n%s", data)
	}
}

func TestFailures(t *testing.T) {
	var stderr bytes.Buffer
	if code := Main([]string{"-t"}, strings.NewReader("Subject: none\n\nx\n"), &stderr); code != exUsage {
		t.Errorf("no recipients: exit %d", code)
	}
	if code := Main([]string{"-bp"}, strings.NewReader(""), &stderr); code != exUsage {
		t.Errorf("queue listing: exit %d", code)
	}
	if code := Main([]string{"-f", "a@b\r\nRCPT TO:<x@y>", "c@d"}, strings.NewReader("x\n"), &stderr); code != exDataErr {
		t.Errorf("injected sender: exit %d", code)
	}
	startServer(t)
	if code := Main([]string{"refused@example.test"}, strings.NewReader("x\n"), &stderr); code != exUnavailable {
		t.Errorf("refused recipient: exit %d", code)
	}
	// No mail server: a quick temporary failure, never a hang.
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	Server = l.Addr().String()
	l.Close()
	if code := Main([]string{"x@example.test"}, strings.NewReader("x\n"), &stderr); code != exTempFail {
		t.Errorf("no server: exit %d", code)
	}
}
