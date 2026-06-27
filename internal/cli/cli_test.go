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

func TestRunInteractiveHelpAndExit(t *testing.T) {
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

	if err := RunInteractive(strings.NewReader("help;\nexit;\n"), &stdout, &stderr); err != nil {
		t.Fatalf("RunInteractive returned error: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{"dmdul: Release v0.1.2", "Copyright (c) 2026 greatfinish", "https://github.com/greatfinish/dmdul", "DMDUL>", "bootstrap;", "list user;", "unload table", "bye"} {
		if !strings.Contains(output, want) {
			t.Fatalf("interactive output should contain %q, got %q", want, output)
		}
	}
}

func TestInteractiveOutputDirDefaultsToDataDirWhenSet(t *testing.T) {
	session := newInteractiveSession()
	if got := session.outputPath("HR_TEST_data.sql"); got != "HR_TEST_data.sql" {
		t.Fatalf("default outputPath = %q", got)
	}
	session.dataDir = `D:\temp\oldpro`
	session.dataDirSet = true
	if got := session.outputPath("HR_TEST_data.sql"); got != `D:\temp\oldpro\HR_TEST_data.sql` {
		t.Fatalf("data_dir outputPath = %q", got)
	}
	session.outputDir = `D:\out`
	session.outputDirSet = true
	if got := session.outputPath("HR_TEST_data.sql"); got != `D:\out\HR_TEST_data.sql` {
		t.Fatalf("explicit output_dir outputPath = %q", got)
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

func TestScanSystemDoesNotRequireControlFile(t *testing.T) {
	dir := t.TempDir()
	systemPath := dir + string(os.PathSeparator) + "SYSTEM.DBF"
	if err := os.WriteFile(systemPath, []byte{0}, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	if err := validateOptionalControlInputFiles("scan-system", systemPath, "", false); err != nil {
		t.Fatalf("scan-system should not require dm.ctl, got %v", err)
	}
}

func TestExportDDLDefaultsRequireOnlySystemFile(t *testing.T) {
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
	for _, want := range []string{"requires -file", "SYSTEM.DBF"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to contain %q, got %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "dm.ctl") {
		t.Fatalf("export-ddl should not require dm.ctl, got %v", err)
	}
}

func TestExportDataDefaultsRequireOnlySystemFile(t *testing.T) {
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
	for _, want := range []string{"requires -file", "SYSTEM.DBF"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to contain %q, got %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "dm.ctl") {
		t.Fatalf("export-data should not require dm.ctl, got %v", err)
	}
}
