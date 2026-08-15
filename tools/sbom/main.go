// Command sbom writes a deterministic CycloneDX inventory of the Go module
// graph. It intentionally uses only the standard library so generation does
// not add a tool dependency to release artifacts.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
)

type module struct {
	Path    string
	Version string
	Main    bool
	Replace *module
}

type component struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	PURL    string `json:"purl,omitempty"`
	BOMRef  string `json:"bom-ref"`
}

type property struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type bom struct {
	BOMFormat    string `json:"bomFormat"`
	SpecVersion  string `json:"specVersion"`
	SerialNumber string `json:"serialNumber"`
	Version      int    `json:"version"`
	Metadata     struct {
		Component  component  `json:"component"`
		Properties []property `json:"properties"`
	} `json:"metadata"`
	Components []component `json:"components"`
}

func serial(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	b := sum[:16]
	b[6] = (b[6] & 0x0f) | 0x50
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return "urn:uuid:" + strings.Join([]string{h[:8], h[8:12], h[12:16], h[16:20], h[20:]}, "-")
}

func purl(path, version string) string {
	if version == "" {
		return "pkg:golang/" + path
	}
	return "pkg:golang/" + path + "@" + version
}

func main() {
	output := flag.String("output", "dist/rclone-123pan.cdx.json", "output CycloneDX JSON path")
	version := flag.String("version", "internal-alpha", "product version")
	commit := flag.String("commit", "unknown", "source commit")
	rcloneVersion := flag.String("rclone-version", "unknown", "pinned rclone version")
	rcloneCommit := flag.String("rclone-commit", "unknown", "pinned rclone commit")
	flag.Parse()

	command := exec.Command("go", "list", "-m", "-json", "all")
	stdout, err := command.StdoutPipe()
	if err != nil {
		panic(err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		panic(err)
	}
	decoder := json.NewDecoder(stdout)
	var modules []module
	for {
		var item module
		err := decoder.Decode(&item)
		if err == io.EOF {
			break
		}
		if err != nil {
			panic(err)
		}
		modules = append(modules, item)
	}
	if err := command.Wait(); err != nil {
		panic(err)
	}

	document := bom{BOMFormat: "CycloneDX", SpecVersion: "1.6", SerialNumber: serial(*version + "|" + *commit), Version: 1}
	document.Metadata.Component = component{Type: "application", Name: "rclone-123pan", Version: *version, BOMRef: "application:rclone-123pan@" + *version}
	document.Metadata.Properties = []property{
		{Name: "org.opencontainers.image.revision", Value: *commit},
		{Name: "rclone.pin", Value: *rcloneVersion + "@" + *rcloneCommit},
	}
	for _, item := range modules {
		if item.Main {
			continue
		}
		actual := item
		if item.Replace != nil {
			actual = *item.Replace
			if actual.Path == "" {
				actual.Path = item.Path
			}
		}
		version := actual.Version
		if version == "" {
			version = item.Version
		}
		ref := purl(actual.Path, version)
		document.Components = append(document.Components, component{Type: "library", Name: actual.Path, Version: version, PURL: ref, BOMRef: ref})
	}
	sort.Slice(document.Components, func(i, j int) bool { return document.Components[i].BOMRef < document.Components[j].BOMRef })

	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		panic(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(*output, data, 0o644); err != nil {
		panic(err)
	}
	fmt.Printf("wrote %s with %d components\n", *output, len(document.Components))
}
