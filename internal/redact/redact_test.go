package redact

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// #114: a log line carrying a session cookie, a bearer token, a JWT or a provider key leaves the process redacted.
func TestLogWriterRedactsCredentials(t *testing.T) {
	var buf bytes.Buffer
	l := log.New(Writer(&buf), "", 0)
	secrets := []string{"Cookie: __Host-antares_session=SESSIONVALUE1234567", "Authorization: Bearer BEARERVALUE12345678",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ4In0.c2lnbmF0dXJlMTIz", "key sk-PROVIDERKEY0123456789abcd", `{"api_key":"KEYVALUE987654"}`, "password=hunter2hunter2"}
	for _, s := range secrets {
		l.Printf("upstream said: %s", s)
	}
	out := buf.String()
	for _, leak := range []string{"SESSIONVALUE1234567", "BEARERVALUE12345678", "eyJzdWIiOiJ4In0", "PROVIDERKEY0123456789abcd", "KEYVALUE987654", "hunter2hunter2"} {
		if strings.Contains(out, leak) {
			t.Errorf("leaked %q:\n%s", leak, out)
		}
	}
	if strings.Count(out, Marker) < len(secrets) || !strings.Contains(out, "__Host-antares_session=") {
		t.Errorf("redaction lost the context around the value:\n%s", out)
	}
}

// Every write path: the app logs only through the log package, and main installs the redacting writer as its output.
// A direct write to stdout or stderr anywhere else in the server would bypass it, so the source tree is checked.
func TestEveryLogPathGoesThroughTheRedactor(t *testing.T) {
	root := filepath.Join("..", "..")
	main, err := os.ReadFile(filepath.Join(root, "cmd", "antares", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(main), "log.SetOutput(redact.Writer(os.Stderr))") {
		t.Fatal("cmd/antares does not install the redacting log writer")
	}
	direct := regexp.MustCompile(`os\.(Stderr|Stdout)\b|fmt\.Print(f|ln)?\(`)
	for _, dir := range []string{"internal", filepath.Join("cmd", "antares")} {
		_ = filepath.WalkDir(filepath.Join(root, dir), func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return err
			}
			// The smoke tool is a separate client program with its own redacted report.
			if strings.Contains(p, string(filepath.Separator)+"smoke"+string(filepath.Separator)) {
				return nil
			}
			src, _ := os.ReadFile(p)
			for i, line := range strings.Split(string(src), "\n") {
				if direct.MatchString(line) && !strings.Contains(line, "log.SetOutput(redact.Writer(os.Stderr))") && !strings.Contains(line, "// stdout: ") {
					t.Errorf("%s:%d writes around the redacting log: %s", p, i+1, strings.TrimSpace(line))
				}
			}
			return nil
		})
	}
}
