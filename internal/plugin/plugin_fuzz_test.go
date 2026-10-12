package plugin

import "testing"

// FuzzParseManifest: no manifest panics the parser, and what it accepts
// is valid.
func FuzzParseManifest(f *testing.F) {
	f.Add([]byte(sample))
	f.Add([]byte("id = \"x\"\nversion = \"1.0.0\"\n[[events]]\non = \"agent.\"\ncommand = [\"./a\"]\ntimeout = \"1s\"\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := ParseManifest(data)
		if err != nil {
			return
		}
		if !idPattern.MatchString(m.ID) || len(m.Startup)+len(m.Actions)+len(m.Events) == 0 {
			t.Fatalf("accepted an invalid manifest: %+v", m)
		}
		for _, a := range m.Actions {
			if a.Timeout() <= 0 || len(a.Command) == 0 {
				t.Fatalf("action %+v", a)
			}
		}
		_ = m.Preview("x")
	})
}
