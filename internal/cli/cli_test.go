package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestRunHelp(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if err := Run([]string{"help"}, &stdout, &stderr); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "dmdul") {
		t.Fatalf("help output should mention dmdul, got %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr should be empty, got %q", stderr.String())
	}
}

func TestInspectRequiresFile(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := Run([]string{"inspect"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("Run should fail when inspect has no -file")
	}
	if !strings.Contains(err.Error(), "requires -file") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInspectCtlRequiresCtl(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := Run([]string{"inspect-ctl"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("Run should fail when inspect-ctl has no -ctl")
	}
	if !strings.Contains(err.Error(), "requires -ctl") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExportDDLDefaultsRequireFileAndCtl(t *testing.T) {
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore Chdir failed: %v", err)
		}
	}()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err = Run([]string{"export-ddl"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("Run should fail when default export input files are absent")
	}
	for _, want := range []string{"requires -file and -ctl", "SYSTEM.DBF", "dm.ctl"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to contain %q, got %v", want, err)
		}
	}
}

func TestExportDataDefaultsRequireFileAndCtl(t *testing.T) {
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore Chdir failed: %v", err)
		}
	}()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err = Run([]string{"export-data"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("Run should fail when default export-data input files are absent")
	}
	for _, want := range []string{"requires -file and -ctl", "SYSTEM.DBF", "dm.ctl"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to contain %q, got %v", want, err)
		}
	}
}
