package ingress

import (
	"context"
	"reflect"
	"testing"
)

func TestValidatorWorkerRunsOnlyFixedNginxChecks(t *testing.T) {
	root := t.TempDir()
	var command []string
	worker := ValidatorWorker{Root: root, Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		command = append([]string{name}, args...)
		return nil, nil
	}}
	if err := worker.validate(context.Background(), validatorRequest{Target: "generation", Generation: 7}); err != nil {
		t.Fatal(err)
	}
	want := []string{"/usr/sbin/nginx", "-t", "-p", root + "/", "-c", "generations/7/nginx.conf"}
	if !reflect.DeepEqual(command, want) {
		t.Fatalf("command = %#v, want %#v", command, want)
	}
	if err := worker.validate(context.Background(), validatorRequest{Target: "../../etc", Generation: 7}); err == nil {
		t.Fatal("accepted an arbitrary validation target")
	}
}

func TestValidatorWorkerChecksOnlyCurrentThroughFixedPath(t *testing.T) {
	root := t.TempDir()
	var command []string
	worker := ValidatorWorker{Root: root, Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		command = append([]string{name}, args...)
		return nil, nil
	}}
	if err := worker.validate(context.Background(), validatorRequest{Target: "current"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"/usr/sbin/nginx", "-t", "-p", root + "/", "-c", "current/nginx.conf"}
	if !reflect.DeepEqual(command, want) {
		t.Fatalf("command = %#v, want %#v", command, want)
	}
}
