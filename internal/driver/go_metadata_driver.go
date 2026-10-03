package driver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"

	"golang.org/x/tools/go/packages"
)

const metadataDriverMarker = "BORK_INTERNAL_METADATA_DRIVER"

// go/packages resolves its Go launcher from the parent's live PATH, not from
// Config.Env. Run its metadata-only builtin driver in a child of this executable
// whose process PATH and driver choice are fixed to the captured context. Types
// remain decoded by the caller; only the documented DriverResponse crosses the
// process boundary. This also works from the driver test executable.
func init() {
	if os.Getenv(metadataDriverMarker) != "1" {
		return
	}
	if err := serveGoMetadataDriver(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func serveGoMetadataDriver() error {
	var request packages.DriverRequest
	if err := json.NewDecoder(os.Stdin).Decode(&request); err != nil {
		return err
	}
	inner := os.Getenv("BORK_INTERNAL_PACKAGE_DRIVER")
	if inner != "off" && inner != "" {
		// Preserve the external driver's raw response, including its compiler,
		// architecture and version. Only NotHandled falls back to builtin Go.
		request.Env = append(request.Env, "GOPACKAGESDRIVER="+inner, metadataDriverMarker+"=0")
		input, err := json.Marshal(request)
		if err != nil {
			return err
		}
		cmd := exec.Command(inner, os.Args[1:]...)
		cmd.Env = append(os.Environ(), "GOPACKAGESDRIVER="+inner, metadataDriverMarker+"=0")
		cmd.Stdin = bytes.NewReader(input)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		output, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("%s: %w: %s", inner, err, stderr.String())
		}
		var status struct{ NotHandled bool }
		if err := json.Unmarshal(output, &status); err != nil {
			return err
		}
		if !status.NotHandled {
			_, err := os.Stdout.Write(output)
			return err
		}
	}
	mode := request.Mode &^ (packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedTypesSizes)
	if request.Mode&(packages.NeedTypes|packages.NeedExportFile) != 0 {
		mode |= packages.NeedName | packages.NeedImports | packages.NeedDeps | packages.NeedExportFile | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedModule
	}
	// Config.Env controls Go settings; this child's own process PATH controls
	// the launcher's lookup, with the captured executable's directory first.
	env := append(request.Env, "GOPACKAGESDRIVER=off", metadataDriverMarker+"=0")
	roots, err := packages.Load(&packages.Config{Mode: mode, Env: env, BuildFlags: request.BuildFlags, Tests: request.Tests, Overlay: request.Overlay}, os.Args[1:]...)
	if err != nil {
		return err
	}
	response := packages.DriverResponse{Compiler: "gc", Arch: os.Getenv("BORK_INTERNAL_GOARCH")}
	version := regexp.MustCompile(`go1\.(\d+)`).FindStringSubmatch(os.Getenv("BORK_INTERNAL_GOVERSION"))
	if len(version) == 2 {
		response.GoVersion, _ = strconv.Atoi(version[1])
	}
	for _, root := range roots {
		response.Roots = append(response.Roots, root.ID)
	}
	seen := map[string]*packages.Package{}
	var collect func(*packages.Package)
	collect = func(pkg *packages.Package) {
		if seen[pkg.ID] != nil {
			return
		}
		seen[pkg.ID] = pkg
		for _, dependency := range pkg.Imports {
			collect(dependency)
		}
	}
	for _, root := range roots {
		collect(root)
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		response.Packages = append(response.Packages, seen[id])
	}
	return json.NewEncoder(os.Stdout).Encode(response)
}

func (ctx *goContext) driverEnv(dir string) ([]string, error) {
	env := append(ctx.metadataEnv(), "GOPACKAGESDRIVER="+ctx.driver)
	if ctx.self != "" && ctx.tool != "" {
		shim, err := ctx.goLauncherShim(dir)
		if err != nil {
			return nil, err
		}
		env = append(env,
			"GOPACKAGESDRIVER="+ctx.self,
			metadataDriverMarker+"=1",
			"BORK_INTERNAL_PACKAGE_DRIVER="+ctx.driver,
			"PATH="+shim+string(os.PathListSeparator)+ctx.processValue("PATH"),
			"BORK_INTERNAL_GOARCH="+ctx.values["GOARCH"],
			"BORK_INTERNAL_GOVERSION="+ctx.values["GOVERSION"],
		)
	}
	return env, nil
}

// Prefix a directory containing only go, preserving auxiliary executable
// selection from the captured PATH. Windows may require copying the launcher
// when symlink creation is unavailable; the captured GOROOT remains explicit.
func (ctx *goContext) goLauncherShim(dir string) (string, error) {
	shim := filepath.Join(dir, "_bork_go_launcher")
	if err := os.Mkdir(shim, 0o700); err != nil {
		return "", err
	}
	name := "go"
	if runtime.GOOS == "windows" {
		name += filepath.Ext(ctx.tool)
	}
	path := filepath.Join(shim, name)
	if err := os.Symlink(ctx.tool, path); err == nil {
		return shim, nil
	} else if runtime.GOOS != "windows" {
		return "", err
	}
	input, err := os.Open(ctx.tool)
	if err != nil {
		return "", err
	}
	defer func() { _ = input.Close() }()
	output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return "", err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return shim, nil
}
