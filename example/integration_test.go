// +build integration_test

package example

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"github.com/jackc/pgx/v4"
	"github.com/jschaf/pggen/internal/errs"
	"github.com/jschaf/pggen/internal/pgdocker"
	"go.uber.org/zap/zaptest"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "update integration tests if true")

const projDir = ".." // hardcoded

// Checks that running pggen doesn't generate a diff.
func TestExamples(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{
			name: "example/author",
			args: []string{
				"--schema-glob", "example/author/schema.sql",
				"--query-glob", "example/author/query.sql",
			},
		},
		{
			name: "example/enums",
			args: []string{
				"--schema-glob", "example/enums/schema.sql",
				"--query-glob", "example/enums/query.sql",
			},
		},
		{
			name: "internal/pg",
			args: []string{
				"--schema-glob", "example/author/schema.sql", // force docker usage
				"--query-glob", "internal/pg/query.sql",
				"--acronym", "oid",
				"--acronym", "oids=OIDs",
			},
		},
		{
			name: "example/device",
			args: []string{
				"--schema-glob", "example/device/schema.sql",
				"--query-glob", "example/device/query.sql",
			},
		},
		{
			name: "example/erp star glob",
			args: []string{
				"--schema-glob", "example/erp/*.sql",
				"--query-glob", "example/erp/order/*.sql",
				"--acronym", "mrr",
			},
		},
		{
			name: "example/erp question marks",
			args: []string{
				"--schema-glob", "example/erp/??_schema.sql",
				"--query-glob", "example/erp/order/*.sql",
				"--acronym", "mrr",
			},
		},
		{
			name: "example/syntax",
			args: []string{
				"--schema-glob", "example/syntax/schema.sql",
				"--query-glob", "example/syntax/query.sql",
			},
		},
		{
			name: "example/custom_types",
			args: []string{
				"--schema-glob", "example/custom_types/schema.sql",
				"--query-glob", "example/custom_types/query.sql",
				"--go-type", "text=github.com/jschaf/pggen/example/custom_types/mytype.String",
				"--go-type", "int8=github.com/jschaf/pggen/example/custom_types.CustomInt",
			},
		},
	}
	if *update {
		// update only disables the assertions. Running the tests causes pggen
		// to overwrite generated code.
		fmt.Println("updating integration test generated files")
	}
	pggen := compilePggen(t)
	// Start a single Docker container to use for all tests. Each test will create
	// a new database in the Postgres cluster.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	docker, err := pgdocker.Start(ctx, nil, zaptest.NewLogger(t).Sugar())
	if err != nil {
		t.Fatal(err)
	}
	defer errs.CaptureT(t, func() error { return docker.Stop(ctx) }, "stop docker")
	mainConnStr, err := docker.ConnString()
	if err != nil {
		t.Fatal(err)
	}
	t.Log("started dockerized postgres: " + mainConnStr)
	conn, err := pgx.Connect(ctx, mainConnStr)
	defer errs.CaptureT(t, func() error { return conn.Close(ctx) }, "close conn")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbName := "pggen_example_" + strconv.FormatInt(int64(rand.Int31()), 36)
			if _, err = conn.Exec(ctx, `CREATE DATABASE `+dbName); err != nil {
				t.Fatal(err)
			}
			connStr := mainConnStr + " dbname=" + dbName
			args := append(tt.args, "--postgres-connection", connStr)
			runPggen(t, pggen, args...)
			if !*update {
				assertNoDiff(t)
			}
		})
	}
}

func runPggen(t *testing.T, pggen string, args ...string) string {
	cmd := exec.Cmd{
		Path: pggen,
		Args: append([]string{pggen, "gen", "go"}, args...),
		Dir:  projDir,
	}
	t.Log("running pggen")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Log("pggen output:\n" + string(bytes.TrimSpace(output)))
		t.Fatalf("run pggen: %s", err)
	}
	return pggen
}

func compilePggen(t *testing.T) string {
	tempDir := t.TempDir()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("lookup go path: %s", err)
	}
	pggen := filepath.Join(tempDir, "pggen")
	cmd := exec.Cmd{
		Path: goBin,
		Args: []string{goBin, "build", "-o", pggen, "./cmd/pggen"},
		Env:  os.Environ(),
		Dir:  projDir,
	}
	t.Log("compiling pggen")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Log("go build output:\n" + string(bytes.TrimSpace(output)))
		t.Fatalf("compile pggen: %s", err)
	}
	return pggen
}

var (
	gitBin     string
	gitBinErr  error
	gitBinOnce = &sync.Once{}
)

func assertNoDiff(t *testing.T) {
	gitBinOnce.Do(func() {
		gitBin, gitBinErr = exec.LookPath("git")
		if gitBinErr != nil {
			gitBinErr = fmt.Errorf("lookup git path: %w", gitBinErr)
		}
	})
	if gitBinErr != nil {
		t.Fatal(gitBinErr)
	}
	updateIndexCmd := exec.Cmd{
		Path: gitBin,
		Args: []string{gitBin, "update-index", "--refresh"},
		Env:  os.Environ(),
		Dir:  projDir,
	}
	updateOutput, err := updateIndexCmd.CombinedOutput()
	if err != nil {
		t.Log("git update-index output:\n" + string(bytes.TrimSpace(updateOutput)))
		t.Fatalf("git update-index: %s", err)
	}
	diffIndexCmd := exec.Cmd{
		Path: gitBin,
		Args: []string{gitBin, "diff-index", "--quiet", "HEAD", "--"},
		Env:  os.Environ(),
		Dir:  projDir,
	}
	diffOutput, err := diffIndexCmd.CombinedOutput()
	if err != nil {
		t.Log("git diff-index output:\n" + string(bytes.TrimSpace(diffOutput)))
		t.Fatalf("git diff-index: %s", err)
	}
}
