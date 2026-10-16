//go:build !windows

package process_test

type testExitError struct {
	code int
}

func (e testExitError) Error() string { return "exit status" }
func (e testExitError) ExitCode() int { return e.code }
