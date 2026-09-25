package backup

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Remote is where backups are copied: an S3-compatible bucket (AWS S3,
// MinIO, Backblaze B2, Wasabi, Cloudflare R2, DigitalOcean Spaces…), an
// SFTP or FTP(S) server, Google Drive, or a local directory (a mounted
// disk or NAS). Credentials are passed by the panel with each call and
// are never stored by the agent.
type Remote struct {
	Type string `json:"type"` // s3 (default), sftp, ftp, gdrive, local

	// S3
	Endpoint  string `json:"endpoint"` // https://s3.eu-west-1.amazonaws.com
	Region    string `json:"region"`
	Bucket    string `json:"bucket"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
	PathStyle bool   `json:"path_style"`

	// Common: a folder inside the storage.
	Prefix string `json:"prefix"`

	// SFTP / FTP
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	PrivateKey string `json:"private_key"` // SFTP, OpenSSH PEM
	HostKey    string `json:"host_key"`    // SFTP, pinned "ssh-ed25519 AAAA…" key
	TLS        string `json:"tls"`         // FTP: explicit (default), implicit, none
	TLSPin     string `json:"tls_pin"`     // FTP: pinned certificate key "sha256//…" (self-signed servers)

	// Google Drive (OAuth refresh token for the drive.file scope)
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RefreshToken string `json:"refresh_token"`
	FolderID     string `json:"folder_id"`

	// Local directory
	Path string `json:"path"`
}

var (
	bucketRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
	prefixRe = regexp.MustCompile(`^[A-Za-z0-9._/-]{0,200}$`)
	regionRe = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)
)

// Validate checks the remote definition.
func (r *Remote) Validate() error {
	r.Prefix = strings.Trim(r.Prefix, "/")
	if !prefixRe.MatchString(r.Prefix) || strings.Contains(r.Prefix, "..") {
		return fmt.Errorf("prefix/folder may contain letters, digits and . _ / -")
	}
	switch r.Type {
	case "", "s3":
		r.Type = "s3"
		return r.validateS3()
	case "sftp", "ftp":
		return r.validateFile()
	case "gdrive":
		return r.validateDrive()
	case "local":
		return r.validateLocal()
	}
	return fmt.Errorf("type must be s3, sftp, ftp, gdrive or local")
}

func (r *Remote) validateS3() error {
	u, err := url.Parse(r.Endpoint)
	if err != nil || u.Host == "" || u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.User != nil {
		return fmt.Errorf("endpoint must be a URL like https://s3.example.com")
	}
	host := u.Hostname()
	local := host == "localhost" || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return fmt.Errorf("endpoint must use https")
	}
	if r.Region == "" {
		r.Region = "us-east-1"
	}
	if !regionRe.MatchString(r.Region) {
		return fmt.Errorf("region is invalid")
	}
	if !bucketRe.MatchString(r.Bucket) {
		return fmt.Errorf("bucket name is invalid")
	}
	if r.AccessKey == "" || r.SecretKey == "" || strings.ContainsAny(r.AccessKey+r.SecretKey, "\n\r ") {
		return fmt.Errorf("access key and secret key are required")
	}
	return nil
}

// Key joins the configured prefix and name.
func (r *Remote) Key(parts ...string) string {
	all := append([]string{}, parts...)
	if r.Prefix != "" {
		all = append([]string{r.Prefix}, all...)
	}
	return strings.Join(all, "/")
}

// S3 is a minimal S3 client.
type S3 struct {
	R      Remote
	Client *http.Client
	Now    func() time.Time
	// PartSize is the multipart chunk size; files larger than it are
	// uploaded in parts.
	PartSize int64
}

// NewS3 returns a client for a validated remote.
func NewS3(r Remote) *S3 {
	return &S3{R: r, Client: &http.Client{Timeout: 30 * time.Minute}, Now: time.Now, PartSize: 64 << 20}
}

func (s *S3) objectURL(key string, q url.Values) *url.URL {
	u, _ := url.Parse(strings.TrimRight(s.R.Endpoint, "/"))
	path := "/" + key
	if s.R.PathStyle {
		path = "/" + s.R.Bucket + path
	} else {
		u.Host = s.R.Bucket + "." + u.Host
	}
	if key == "" {
		path = strings.TrimSuffix(path, "/")
		if path == "" {
			path = "/"
		}
	}
	u.Path = path
	u.RawPath = uriEncode(path, false)
	u.RawQuery = canonicalQuery(q)
	return u
}

func uriEncode(s string, encodeSlash bool) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/' && !encodeSlash:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func canonicalQuery(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		for _, v := range q[k] {
			parts = append(parts, uriEncode(k, true)+"="+uriEncode(v, true))
		}
	}
	return strings.Join(parts, "&")
}

func hmacSHA(key []byte, s string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(s))
	return m.Sum(nil)
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// sign adds AWS Signature Version 4 headers to req.
func (s *S3) sign(req *http.Request, payloadHash string) {
	t := s.Now().UTC()
	amzDate := t.Format("20060102T150405Z")
	date := t.Format("20060102")
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	names := []string{"host"}
	for k := range req.Header {
		lk := strings.ToLower(k)
		if lk == "x-amz-date" || lk == "x-amz-content-sha256" || lk == "range" || lk == "content-type" || lk == "content-md5" {
			names = append(names, lk)
		}
	}
	sort.Strings(names)
	var ch strings.Builder
	for _, n := range names {
		v := req.Host
		if v == "" {
			v = req.URL.Host
		}
		if n != "host" {
			v = strings.TrimSpace(req.Header.Get(n))
		}
		ch.WriteString(n + ":" + v + "\n")
	}
	signed := strings.Join(names, ";")
	canonical := strings.Join([]string{req.Method, req.URL.EscapedPath(), req.URL.RawQuery, ch.String(), signed, payloadHash}, "\n")
	scope := date + "/" + s.R.Region + "/s3/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + sha256Hex([]byte(canonical))
	k := hmacSHA([]byte("AWS4"+s.R.SecretKey), date)
	k = hmacSHA(k, s.R.Region)
	k = hmacSHA(k, "s3")
	k = hmacSHA(k, "aws4_request")
	sig := hex.EncodeToString(hmacSHA(k, toSign))
	req.Header.Set("Authorization", fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s", s.R.AccessKey, scope, signed, sig))
}

func (s *S3) do(ctx context.Context, method, key string, q url.Values, body []byte, bodyReader io.Reader, size int64, payloadHash string) (*http.Response, error) {
	u := s.objectURL(key, q)
	var r io.Reader
	if bodyReader != nil {
		r = bodyReader
	} else if body != nil {
		r = bytes.NewReader(body)
		size = int64(len(body))
		payloadHash = sha256Hex(body)
	}
	if payloadHash == "" {
		payloadHash = sha256Hex(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), r)
	if err != nil {
		return nil, err
	}
	req.URL = u
	if r != nil {
		req.ContentLength = size
	}
	s.sign(req, payloadHash)
	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("storage request failed: %s", redactURL(err.Error()))
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		var e struct {
			Code    string `xml:"Code"`
			Message string `xml:"Message"`
		}
		_ = xml.Unmarshal(b, &e)
		return nil, fmt.Errorf("storage returned %d %s %s", resp.StatusCode, e.Code, e.Message)
	}
	return resp, nil
}

func redactURL(s string) string {
	return regexp.MustCompile(`X-Amz-[A-Za-z-]+=[^&\s"]+`).ReplaceAllString(s, "X-Amz-…")
}

// Put uploads a local file, using multipart upload when it is large.
func (s *S3) Put(ctx context.Context, key, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() <= s.PartSize {
		h, err := hashSection(f, 0, info.Size())
		if err != nil {
			return err
		}
		resp, err := s.do(ctx, http.MethodPut, key, nil, nil, io.NewSectionReader(f, 0, info.Size()), info.Size(), h)
		if err != nil {
			return err
		}
		return resp.Body.Close()
	}
	resp, err := s.do(ctx, http.MethodPost, key, url.Values{"uploads": {""}}, []byte{}, nil, 0, "")
	if err != nil {
		return err
	}
	var init struct {
		UploadID string `xml:"UploadId"`
	}
	err = xml.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&init)
	resp.Body.Close()
	if err != nil || init.UploadID == "" {
		return fmt.Errorf("storage did not start the multipart upload")
	}
	abort := func() {
		if r, err := s.do(context.Background(), http.MethodDelete, key, url.Values{"uploadId": {init.UploadID}}, nil, nil, 0, ""); err == nil {
			r.Body.Close()
		}
	}
	type part struct {
		Number int    `xml:"PartNumber"`
		ETag   string `xml:"ETag"`
	}
	var parts []part
	for off, n := int64(0), 1; off < info.Size(); off, n = off+s.PartSize, n+1 {
		size := min(s.PartSize, info.Size()-off)
		h, err := hashSection(f, off, size)
		if err != nil {
			abort()
			return err
		}
		q := url.Values{"partNumber": {strconv.Itoa(n)}, "uploadId": {init.UploadID}}
		resp, err := s.do(ctx, http.MethodPut, key, q, nil, io.NewSectionReader(f, off, size), size, h)
		if err != nil {
			abort()
			return err
		}
		parts = append(parts, part{n, resp.Header.Get("ETag")})
		resp.Body.Close()
	}
	body, _ := xml.Marshal(struct {
		XMLName xml.Name `xml:"CompleteMultipartUpload"`
		Parts   []part   `xml:"Part"`
	}{Parts: parts})
	resp, err = s.do(ctx, http.MethodPost, key, url.Values{"uploadId": {init.UploadID}}, body, nil, 0, "")
	if err != nil {
		abort()
		return err
	}
	// S3 can report a failed completion with 200 and an <Error> body.
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if bytes.Contains(b, []byte("<Error>")) {
		abort()
		return fmt.Errorf("storage could not complete the upload")
	}
	return nil
}

func hashSection(f *os.File, off, size int64) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(f, off, size)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Get downloads an object to w.
func (s *S3) Get(ctx context.Context, key string, w io.Writer) error {
	resp, err := s.do(ctx, http.MethodGet, key, nil, nil, nil, 0, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(w, resp.Body)
	return err
}

// Delete removes an object (missing objects are not an error).
func (s *S3) Delete(ctx context.Context, key string) error {
	resp, err := s.do(ctx, http.MethodDelete, key, nil, nil, nil, 0, "")
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			return nil
		}
		return err
	}
	return resp.Body.Close()
}

// List returns keys under prefix (at most 1000 per page, all pages).
func (s *S3) List(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	token := ""
	for {
		q := url.Values{"list-type": {"2"}, "prefix": {prefix}}
		if token != "" {
			q.Set("continuation-token", token)
		}
		resp, err := s.do(ctx, http.MethodGet, "", q, nil, nil, 0, "")
		if err != nil {
			return nil, err
		}
		var out struct {
			Contents []struct {
				Key string `xml:"Key"`
			} `xml:"Contents"`
			IsTruncated bool   `xml:"IsTruncated"`
			Next        string `xml:"NextContinuationToken"`
		}
		err = xml.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&out)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("storage returned an unreadable listing")
		}
		for _, c := range out.Contents {
			keys = append(keys, c.Key)
		}
		if !out.IsTruncated || out.Next == "" || len(keys) > 100000 {
			return keys, nil
		}
		token = out.Next
	}
}

// RemoveDir is a no-op: S3 has no folders.
func (s *S3) RemoveDir(ctx context.Context, prefix string) error { return nil }
