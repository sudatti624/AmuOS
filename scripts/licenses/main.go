// Command licenses writes the license texts of everything compiled into the
// control plane: the Go standard library, Go module dependencies and the
// bundled web dependencies. Release archives include its output.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

func run() error {
	out := flag.String("out", "THIRD_PARTY_LICENSES.md", "output file")
	web := flag.String("web", "internal/webui/static/app/.vite/license.md", "license file written by the web build")
	flag.Parse()

	var b bytes.Buffer
	b.WriteString("# Third-party licenses\n\nAmuOS includes the following software. Each component is used under its own license, reproduced below.\n")

	goroot, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		return fmt.Errorf("go env GOROOT: %w", err)
	}
	if err = section(&b, "Go standard library", []string{filepath.Join(strings.TrimSpace(string(goroot)), "LICENSE")}); err != nil {
		return err
	}

	list, err := exec.Command("go", "list", "-deps", "-f", "{{with .Module}}{{if not .Main}}{{.Path}} {{.Version}}\t{{.Dir}}{{end}}{{end}}", "./cmd/controlplane").Output()
	if err != nil {
		return fmt.Errorf("go list: %w", err)
	}
	modules := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(list))
	for scanner.Scan() {
		if name, dir, ok := strings.Cut(scanner.Text(), "\t"); ok {
			modules[name] = dir
		}
	}
	names := make([]string, 0, len(modules))
	for name := range modules {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		files, err := licenseFiles(modules[name])
		if err != nil {
			return err
		}
		if len(files) == 0 {
			return fmt.Errorf("no license file found for %s", name)
		}
		if err = section(&b, name, files); err != nil {
			return err
		}
	}

	webLicenses, err := os.ReadFile(*web)
	if err != nil {
		return fmt.Errorf("web licenses (build web/ first): %w", err)
	}
	b.WriteString("\n---\n\n## Web UI dependencies\n\n")
	b.Write(webLicenses)
	return os.WriteFile(*out, b.Bytes(), 0o644)
}

// licenseFiles returns top-level LICENSE, LICENCE, COPYING and NOTICE files.
func licenseFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		upper := strings.ToUpper(e.Name())
		if !e.IsDir() && (strings.HasPrefix(upper, "LICENSE") || strings.HasPrefix(upper, "LICENCE") || strings.HasPrefix(upper, "COPYING") || strings.HasPrefix(upper, "NOTICE")) {
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	return files, nil
}

func section(b *bytes.Buffer, title string, files []string) error {
	fmt.Fprintf(b, "\n---\n\n## %s\n", title)
	for _, file := range files {
		text, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		// Four backticks, because some license files contain ``` themselves.
		fmt.Fprintf(b, "\n### %s\n\n````text\n%s\n````\n", filepath.Base(file), strings.TrimRight(string(text), "\n"))
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "licenses:", err)
		os.Exit(1)
	}
}
