package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Validator struct {
	filename string
	errors   []string
}

func (v *Validator) errorf(line int, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	baseName := filepath.Base(v.filename)
	if line > 0 {
		v.errors = append(v.errors, fmt.Sprintf("%s:%d %s", baseName, line, msg))
	} else {
		v.errors = append(v.errors, fmt.Sprintf("%s %s", baseName, msg))
	}
}

func (v *Validator) validateTopLevel(doc *yaml.Node) {
	if doc.Kind != yaml.MappingNode {
		v.errorf(doc.Line, "root must be a mapping")
		return
	}

	fields := v.toMap(doc)
	required := []string{"apiVersion", "kind", "metadata", "spec"}

	for _, f := range required {
		if node, ok := fields[f]; !ok {
			v.errorf(doc.Line, "%s is required", f)
		} else {
			switch f {
			case "apiVersion":
				if node.Value != "v1" {
					v.errorf(node.Line, "apiVersion has unsupported value '%s'", node.Value)
				}
			case "kind":
				if node.Value != "Pod" {
					v.errorf(node.Line, "kind has unsupported value '%s'", node.Value)
				}
			case "metadata":
				v.validateMetadata(node)
			case "spec":
				v.validateSpec(node)
			}
		}
	}
}

func (v *Validator) toMap(node *yaml.Node) map[string]*yaml.Node {
	if node.Kind != yaml.MappingNode {
		return nil
	}
	m := make(map[string]*yaml.Node)
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i]
		val := node.Content[i+1]
		m[key.Value] = val
	}
	return m
}

func (v *Validator) validateMetadata(node *yaml.Node) {
	fields := v.toMap(node)
	if fields == nil {
		v.errorf(node.Line, "metadata must be a mapping")
		return
	}

	nameNode, ok := fields["name"]
	if !ok || nameNode.Value == "" {
		if !ok {
			v.errorf(node.Line, "name is required")
		} else {
			v.errorf(nameNode.Line, "name is required")
		}
	} else {
		v.validateContainerName(nameNode, nil) // uniqueness не проверяем здесь
	}

	if ns, ok := fields["namespace"]; ok {
		if ns.Kind != yaml.ScalarNode {
			v.errorf(ns.Line, "namespace must be string")
		}
	}
	if labels, ok := fields["labels"]; ok {
		if labels.Kind != yaml.MappingNode {
			v.errorf(labels.Line, "labels must be a mapping")
		}
	}
}

func (v *Validator) validateSpec(node *yaml.Node) {
	fields := v.toMap(node)
	if fields == nil {
		v.errorf(node.Line, "spec must be a mapping")
		return
	}

	containers, ok := fields["containers"]
	if !ok {
		v.errorf(node.Line, "containers is required")
	} else {
		v.validateContainers(containers)
	}

	if osNode, ok := fields["os"]; ok {
		v.validatePodOS(osNode)
	}
}

func (v *Validator) validatePodOS(node *yaml.Node) {
	fields := v.toMap(node)
	if fields == nil {
		v.errorf(node.Line, "os must be a mapping")
		return
	}

	nameNode, ok := fields["name"]
	if !ok {
		v.errorf(node.Line, "os.name is required")
	} else if nameNode.Value != "linux" && nameNode.Value != "windows" {
		v.errorf(nameNode.Line, "os has unsupported value '%s'", nameNode.Value)
	}
}

func (v *Validator) validateContainers(node *yaml.Node) {
	if node.Kind != yaml.SequenceNode {
		v.errorf(node.Line, "containers must be a sequence")
		return
	}

	names := make(map[string]bool)
	for _, c := range node.Content {
		v.validateContainer(c, names)
	}
}

func (v *Validator) validateContainer(node *yaml.Node, names map[string]bool) {
	fields := v.toMap(node)
	if fields == nil {
		v.errorf(node.Line, "container must be a mapping")
		return
	}

	// name
	nameNode, ok := fields["name"]
	if !ok || nameNode.Value == "" {
		if !ok {
			v.errorf(node.Line, "name is required")
		} else {
			v.errorf(nameNode.Line, "name is required")
		}
	} else {
		v.validateContainerName(nameNode, names)
	}

	// image
	imgNode, ok := fields["image"]
	if !ok || imgNode.Value == "" {
		if !ok {
			v.errorf(node.Line, "image is required")
		} else {
			v.errorf(imgNode.Line, "image is required")
		}
	} else {
		v.validateImage(imgNode)
	}

	// resources
	resNode, ok := fields["resources"]
	if !ok {
		v.errorf(node.Line, "resources is required")
	} else {
		v.validateResources(resNode)
	}

	// ports
	if ports, ok := fields["ports"]; ok {
		v.validatePorts(ports)
	}

	// probes
	for _, pname := range []string{"readinessProbe", "livenessProbe"} {
		if probe, ok := fields[pname]; ok {
			v.validateProbe(probe)
		}
	}
}

func (v *Validator) validateContainerName(node *yaml.Node, names map[string]bool) {
	if node.Kind != yaml.ScalarNode {
		v.errorf(node.Line, "name must be string")
		return
	}
	if node.Value == "" {
		v.errorf(node.Line, "name is required")
		return
	}
	if !regexp.MustCompile(`^[a-z][a-z0-9_]*$`).MatchString(node.Value) {
		v.errorf(node.Line, "name has invalid format '%s'", node.Value)
		return
	}
	if names != nil {
		if names[node.Value] {
			v.errorf(node.Line, "name '%s' is not unique", node.Value)
		}
		names[node.Value] = true
	}
}

func (v *Validator) validateImage(node *yaml.Node) {
	if node.Kind != yaml.ScalarNode {
		v.errorf(node.Line, "image must be string")
		return
	}
	if !strings.HasPrefix(node.Value, "registry.bigbrother.io/") || !strings.Contains(node.Value, ":") {
		v.errorf(node.Line, "image has invalid format '%s'", node.Value)
	}
}

func (v *Validator) validatePorts(node *yaml.Node) {
	if node.Kind != yaml.SequenceNode {
		v.errorf(node.Line, "ports must be a sequence")
		return
	}
	for _, p := range node.Content {
		v.validateContainerPort(p)
	}
}

func (v *Validator) validateContainerPort(node *yaml.Node) {
	fields := v.toMap(node)
	if fields == nil {
		v.errorf(node.Line, "port must be a mapping")
		return
	}

	cpNode, ok := fields["containerPort"]
	if !ok {
		v.errorf(node.Line, "containerPort is required")
	} else {
		v.validatePort(cpNode, "containerPort")
	}

	if proto, ok := fields["protocol"]; ok {
		if proto.Value != "TCP" && proto.Value != "UDP" {
			v.errorf(proto.Line, "protocol has unsupported value '%s'", proto.Value)
		}
	}
}

func (v *Validator) validateProbe(node *yaml.Node) {
	fields := v.toMap(node)
	if fields == nil {
		v.errorf(node.Line, "probe must be a mapping")
		return
	}

	httpGet, ok := fields["httpGet"]
	if !ok {
		v.errorf(node.Line, "httpGet is required")
	} else {
		v.validateHTTPGet(httpGet)
	}
}

func (v *Validator) validateHTTPGet(node *yaml.Node) {
	fields := v.toMap(node)
	if fields == nil {
		v.errorf(node.Line, "httpGet must be a mapping")
		return
	}

	pathNode, ok := fields["path"]
	if !ok {
		v.errorf(node.Line, "path is required")
	} else if !strings.HasPrefix(pathNode.Value, "/") {
		v.errorf(pathNode.Line, "path has invalid format '%s'", pathNode.Value)
	}

	portNode, ok := fields["port"]
	if !ok {
		v.errorf(node.Line, "port is required")
	} else {
		v.validatePort(portNode, "port")
	}
}

func (v *Validator) validateResources(node *yaml.Node) {
	fields := v.toMap(node)
	if fields == nil {
		v.errorf(node.Line, "resources must be a mapping")
		return
	}

	for _, key := range []string{"limits", "requests"} {
		if r, ok := fields[key]; ok {
			v.validateResourceMap(r)
		}
	}
}

func (v *Validator) validateResourceMap(node *yaml.Node) {
	fields := v.toMap(node)
	if fields == nil {
		v.errorf(node.Line, "resource section must be a mapping")
		return
	}

	if cpu, ok := fields["cpu"]; ok {
		// КЛЮЧЕВОЕ: только !!int разрешён
		if cpu.Tag != "!!int" {
			v.errorf(cpu.Line, "cpu must be int")
		} else if _, err := strconv.Atoi(cpu.Value); err != nil {
			v.errorf(cpu.Line, "cpu must be int")
		}
	}

	if mem, ok := fields["memory"]; ok {
		if mem.Kind != yaml.ScalarNode {
			v.errorf(mem.Line, "memory must be string")
		} else if !regexp.MustCompile(`^\d+(Gi|Mi|Ki)$`).MatchString(mem.Value) {
			v.errorf(mem.Line, "memory has invalid format '%s'", mem.Value)
		}
	}
}

func (v *Validator) validatePort(node *yaml.Node, field string) {
	// Должно быть целое число (в YAML — !!int)
	if node.Tag != "!!int" {
		v.errorf(node.Line, "%s must be int", field)
		return
	}
	port, err := strconv.Atoi(node.Value)
	if err != nil {
		v.errorf(node.Line, "%s must be int", field)
		return
	}
	if port <= 0 || port >= 65536 {
		v.errorf(node.Line, "%s value out of range", field)
	}
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: yamlvalidator <file>\n")
		os.Exit(1)
	}

	filename := os.Args[1]
	content, err := os.ReadFile(filename)
	if err != nil {
		fmt.Printf("%s: cannot read file: %v\n", filepath.Base(filename), err)
		os.Exit(1)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		fmt.Printf("%s: cannot unmarshal YAML: %v\n", filepath.Base(filename), err)
		os.Exit(1)
	}

	if len(root.Content) == 0 {
		fmt.Printf("%s: empty YAML\n", filepath.Base(filename))
		os.Exit(1)
	}

	validator := &Validator{filename: filename}
	validator.validateTopLevel(root.Content[0])

	if len(validator.errors) > 0 {
		for _, err := range validator.errors {
			fmt.Println(err) // stdout!
		}
		os.Exit(1)
	}
}