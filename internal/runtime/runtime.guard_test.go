package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/admirable-oss/hive/internal/environment"
)

type recordingEnvs struct {
	environment.Service // nil: only Delete is exercised
	calls               *[]string
}

func (r recordingEnvs) Delete(_ context.Context, id string) error {
	*r.calls = append(*r.calls, "delete "+id)
	return nil
}

type recordingProcs struct {
	calls *[]string
	err   error
}

func (r recordingProcs) StopEnvironment(_ context.Context, id string) error {
	*r.calls = append(*r.calls, "stop "+id)
	return r.err
}

func TestEnvGuardStopsAgentsBeforeDeleting(t *testing.T) {
	var calls []string
	g := envGuard{Service: recordingEnvs{calls: &calls}, procs: recordingProcs{calls: &calls}}
	if err := g.Delete(context.Background(), "dev"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0] != "stop dev" || calls[1] != "delete dev" {
		t.Fatalf("calls = %v, want stop then delete", calls)
	}
}

func TestEnvGuardKeepsEnvironmentWhenStopFails(t *testing.T) {
	var calls []string
	boom := errors.New("agents would not stop")
	g := envGuard{Service: recordingEnvs{calls: &calls}, procs: recordingProcs{calls: &calls, err: boom}}
	if err := g.Delete(context.Background(), "dev"); !errors.Is(err, boom) {
		t.Fatalf("got %v, want the stop error", err)
	}
	if len(calls) != 1 {
		t.Fatalf("delete must not run after a failed stop; calls = %v", calls)
	}
}
