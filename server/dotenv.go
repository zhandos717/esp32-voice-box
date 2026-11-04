package main

// A tiny .env loader: KEY=VALUE lines, # comments, optional quotes. Values already set in the environment win.
// The file is searched in the working directory, next to the program and one folder above it (the project root).
// MIC_NO_DOTENV=1 turns it off (the tests use it, so they never touch real secrets).

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

func loadDotEnv() {
	if os.Getenv("MIC_NO_DOTENV") != "" {
		return
	}
	exe, _ := os.Executable()
	dir := filepath.Dir(exe)
	wd, _ := os.Getwd()
	for _, p := range []string{filepath.Join(wd, ".env"), filepath.Join(dir, ".env"), filepath.Join(dir, "..", ".env")} {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			line = strings.TrimPrefix(line, "export ")
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			k, v = strings.TrimSpace(k), strings.TrimSpace(v)
			if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
				v = v[1 : len(v)-1]
			}
			if k != "" && os.Getenv(k) == "" {
				os.Setenv(k, v)
			}
		}
		f.Close()
		return // the first .env found is enough
	}
}
