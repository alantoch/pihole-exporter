package main

import (
	"bytes"
	"cmp"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

type generator struct {
	root string
	docs map[string]map[string]any
}

type metricSpec struct {
	Name     string
	Help     string
	Endpoint string
	Path     []string
	Label    string
}

func main() {
	specPath := flag.String("spec", "", "OpenAPI main.yaml path")
	outPath := flag.String("out", "", "generated Go output path")
	packageName := flag.String("package", "pihole", "generated Go package name")
	flag.Parse()

	if *specPath == "" || *outPath == "" {
		log.Fatal("-spec and -out are required")
	}

	gen := &generator{
		root: filepath.Dir(*specPath),
		docs: make(map[string]map[string]any),
	}

	specs, apiVersion, err := gen.generate(*specPath)
	if err != nil {
		log.Fatal(err)
	}

	if len(specs) == 0 {
		log.Fatal("no metrics generated")
	}

	source, err := render(*packageName, apiVersion, *specPath, specs)
	if err != nil {
		log.Fatal(err)
	}

	if err := os.WriteFile(*outPath, source, 0o644); err != nil {
		log.Fatal(err)
	}
}

func (g *generator) generate(specPath string) ([]metricSpec, string, error) {
	doc, err := g.load(specPath)
	if err != nil {
		return nil, "", err
	}

	apiVersion, _ := stringAt(doc, "info", "version")
	paths, ok := mapAt(doc, "paths")
	if !ok {
		return nil, "", errors.New("main spec has no paths")
	}

	var specs []metricSpec
	for endpoint, rawPath := range paths {
		if !strings.HasPrefix(endpoint, "/stats/") {
			continue
		}
		if strings.HasPrefix(endpoint, "/stats/database/") {
			continue
		}

		pathItem, ok := rawPath.(map[string]any)
		if !ok {
			continue
		}

		resolved, err := g.resolve(pathItem, specPath)
		if err != nil {
			return nil, "", fmt.Errorf("resolve %s: %w", endpoint, err)
		}

		get, ok := mapAt(resolved, "get")
		if !ok || !hasTag(get, "Metrics") {
			continue
		}

		schema, ok := mapAt(get, "responses", "200", "content", "application/json", "schema")
		if !ok {
			continue
		}

		collected, err := g.collectSchema(endpoint, nil, "", schema, specPath)
		if err != nil {
			return nil, "", fmt.Errorf("collect %s: %w", endpoint, err)
		}
		specs = append(specs, collected...)
	}

	slices.SortFunc(specs, func(a, b metricSpec) int {
		return cmp.Or(
			cmp.Compare(a.Endpoint, b.Endpoint),
			cmp.Compare(strings.Join(a.Path, "."), strings.Join(b.Path, ".")),
			cmp.Compare(a.Label, b.Label),
		)
	})

	return specs, apiVersion, nil
}

func (g *generator) collectSchema(endpoint string, path []string, parentHelp string, schema map[string]any, currentFile string) ([]metricSpec, error) {
	resolved, err := g.resolve(schema, currentFile)
	if err != nil {
		return nil, err
	}

	if refs, ok := listAt(resolved, "allOf"); ok {
		var specs []metricSpec
		for _, ref := range refs {
			child, ok := ref.(map[string]any)
			if !ok {
				continue
			}
			collected, err := g.collectSchema(endpoint, path, parentHelp, child, currentFile)
			if err != nil {
				return nil, err
			}
			specs = append(specs, collected...)
		}
		return specs, nil
	}

	schemaType, _ := stringAt(resolved, "type")
	description, _ := stringAt(resolved, "description")
	if description == "" {
		description = parentHelp
	}

	switch schemaType {
	case "integer", "number", "boolean":
		if shouldSkipPath(path) {
			return nil, nil
		}
		return []metricSpec{{
			Name:     metricName(endpoint, path, ""),
			Help:     helpText(description, path),
			Endpoint: endpoint,
			Path:     slices.Clone(path),
		}}, nil
	case "object", "":
		properties, ok := mapAt(resolved, "properties")
		if !ok {
			return nil, nil
		}

		if label, ok := labelForNumericObject(path, properties); ok {
			return []metricSpec{{
				Name:     metricName(endpoint, path, label),
				Help:     helpText(description, path),
				Endpoint: endpoint,
				Path:     slices.Clone(path),
				Label:    label,
			}}, nil
		}

		keys := make([]string, 0, len(properties))
		for key := range properties {
			keys = append(keys, key)
		}
		slices.Sort(keys)

		var specs []metricSpec
		for _, key := range keys {
			child, ok := properties[key].(map[string]any)
			if !ok {
				continue
			}
			collected, err := g.collectSchema(endpoint, append(slices.Clone(path), key), description, child, currentFile)
			if err != nil {
				return nil, err
			}
			specs = append(specs, collected...)
		}
		return specs, nil
	case "array":
		return nil, nil
	default:
		return nil, nil
	}
}

func (g *generator) resolve(value map[string]any, currentFile string) (map[string]any, error) {
	ref, ok := stringAt(value, "$ref")
	if !ok {
		return value, nil
	}

	refFile, pointer, ok := strings.Cut(ref, "#")
	if !ok {
		return nil, fmt.Errorf("unsupported ref %q", ref)
	}
	if refFile == "" {
		refFile = currentFile
	} else if isURL(currentFile) {
		currentURL, err := url.Parse(currentFile)
		if err != nil {
			return nil, err
		}
		refURL, err := currentURL.Parse(refFile)
		if err != nil {
			return nil, err
		}
		refFile = refURL.String()
	} else if !filepath.IsAbs(refFile) {
		refFile = filepath.Join(g.root, refFile)
	}

	doc, err := g.load(refFile)
	if err != nil {
		return nil, err
	}

	resolved, err := jsonPointer(doc, pointer)
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", ref, err)
	}

	asMap, ok := resolved.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("ref %q does not resolve to an object", ref)
	}

	return asMap, nil
}

func (g *generator) load(path string) (map[string]any, error) {
	key := path
	if !isURL(path) {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		key = abs
	}

	if doc, ok := g.docs[key]; ok {
		return doc, nil
	}

	data, err := readSpec(path)
	if err != nil {
		return nil, err
	}

	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	g.docs[key] = doc
	return doc, nil
}

func readSpec(path string) ([]byte, error) {
	if !isURL(path) {
		return os.ReadFile(path)
	}

	resp, err := http.Get(path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("download %s: %s", path, resp.Status)
	}

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func render(packageName, apiVersion, specPath string, specs []metricSpec) ([]byte, error) {
	var buf bytes.Buffer

	fmt.Fprintf(&buf, "package %s\n\n", packageName)
	fmt.Fprintln(&buf, "// Code generated by tools/pihole-metricgen; DO NOT EDIT.")
	fmt.Fprintf(&buf, "// Source: %s\n", filepath.ToSlash(specPath))
	fmt.Fprintln(&buf, "// Metrics are compiled from Pi-hole OpenAPI Metrics schemas.")
	fmt.Fprintf(&buf, "const CompiledPiHoleAPIVersion = %q\n\n", apiVersion)
	fmt.Fprintln(&buf, "var compiledMetricSpecs = []MetricSpec{")

	for _, spec := range specs {
		fmt.Fprintln(&buf, "\t{")
		fmt.Fprintf(&buf, "\t\tName: %q,\n", spec.Name)
		fmt.Fprintf(&buf, "\t\tHelp: %q,\n", spec.Help)
		fmt.Fprintln(&buf, "\t\tKind: MetricKindGauge,")
		fmt.Fprintf(&buf, "\t\tEndpoint: %q,\n", spec.Endpoint)
		fmt.Fprint(&buf, "\t\tPath: []string{")
		for index, part := range spec.Path {
			if index > 0 {
				fmt.Fprint(&buf, ", ")
			}
			fmt.Fprintf(&buf, "%q", part)
		}
		fmt.Fprintln(&buf, "},")
		if spec.Label != "" {
			fmt.Fprintf(&buf, "\t\tLabel: %q,\n", spec.Label)
		}
		fmt.Fprintln(&buf, "\t},")
	}

	fmt.Fprintln(&buf, "}")

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, err
	}

	return formatted, nil
}

func labelForNumericObject(path []string, properties map[string]any) (string, bool) {
	if len(path) == 0 || len(properties) == 0 {
		return "", false
	}

	var label string
	last := path[len(path)-1]
	switch last {
	case "types":
		label = "type"
	case "status":
		label = "status"
	case "replies":
		label = "reply"
	default:
		return "", false
	}

	for _, raw := range properties {
		child, ok := raw.(map[string]any)
		if !ok {
			return "", false
		}
		childType, _ := stringAt(child, "type")
		if childType != "integer" && childType != "number" && childType != "boolean" {
			return "", false
		}
	}

	return label, true
}

func metricName(endpoint string, path []string, label string) string {
	parts := []string{"pihole"}
	for _, part := range strings.Split(strings.Trim(endpoint, "/"), "/") {
		if part == "" || part == "stats" {
			continue
		}
		parts = append(parts, part)
	}

	for index, part := range path {
		if label != "" && index == len(path)-1 {
			parts = append(parts, "by", label)
			continue
		}
		parts = append(parts, part)
	}

	if len(path) > 0 && path[len(path)-1] == "last_update" {
		parts = append(parts, "timestamp", "seconds")
	}

	return sanitize(strings.Join(parts, "_"))
}

func helpText(description string, path []string) string {
	description = strings.TrimSpace(strings.ReplaceAll(description, "\n", " "))
	if description != "" {
		return description
	}
	if len(path) == 0 {
		return "Pi-hole metric"
	}
	return "Pi-hole " + strings.ReplaceAll(strings.Join(path, " "), "_", " ")
}

func sanitize(value string) string {
	var out strings.Builder
	lastUnderscore := false
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			out.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(out.String(), "_")
}

func singular(value string) string {
	if strings.HasSuffix(value, "ies") {
		return strings.TrimSuffix(value, "ies") + "y"
	}
	if strings.HasSuffix(value, "s") {
		return strings.TrimSuffix(value, "s")
	}
	return value
}

func shouldSkipPath(path []string) bool {
	return len(path) == 1 && path[0] == "took"
}

func isURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

func hasTag(operation map[string]any, want string) bool {
	tags, ok := listAt(operation, "tags")
	if !ok {
		return false
	}
	for _, tag := range tags {
		if tag == want {
			return true
		}
	}
	return false
}

func jsonPointer(root any, pointer string) (any, error) {
	pointer = strings.TrimPrefix(pointer, "/")
	if pointer == "" {
		return root, nil
	}

	current := root
	for _, part := range strings.Split(pointer, "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		obj, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%q is not an object", part)
		}
		current, ok = obj[part]
		if !ok {
			return nil, fmt.Errorf("%q not found", part)
		}
	}
	return current, nil
}

func mapAt(root map[string]any, path ...string) (map[string]any, bool) {
	var current any = root
	for _, part := range path {
		obj, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = obj[part]
		if !ok {
			return nil, false
		}
	}
	out, ok := current.(map[string]any)
	return out, ok
}

func listAt(root map[string]any, path ...string) ([]any, bool) {
	var current any = root
	for _, part := range path {
		obj, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = obj[part]
		if !ok {
			return nil, false
		}
	}
	out, ok := current.([]any)
	return out, ok
}

func stringAt(root map[string]any, path ...string) (string, bool) {
	var current any = root
	for _, part := range path {
		obj, ok := current.(map[string]any)
		if !ok {
			return "", false
		}
		current, ok = obj[part]
		if !ok {
			return "", false
		}
	}
	out, ok := current.(string)
	return out, ok
}
