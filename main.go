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

type ValidationError struct {
	Line    int
	Message string
}

var snakeCaseRegex = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
var memoryRegex = regexp.MustCompile(`^\d+(Gi|Mi|Ki)$`)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: yamlvalid <path-to-yaml-file>")
		os.Exit(1)
	}

	filename := os.Args[1]
	basename := filepath.Base(filename)
	
	content, err := os.ReadFile(filename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot read file: %v\n", err)
		os.Exit(1)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		fmt.Fprintf(os.Stderr, "cannot unmarshal YAML: %v\n", err)
		os.Exit(1)
	}

	if len(root.Content) == 0 {
		fmt.Fprintln(os.Stderr, "empty YAML file")
		os.Exit(1)
	}

	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		fmt.Fprintf(os.Stderr, "%s:%d root must be a mapping\n", basename, doc.Line)
		os.Exit(1)
	}

	errors := validatePod(doc, basename)
	
	for _, errMsg := range errors {
		fmt.Fprintf(os.Stderr, "%s\n", errMsg)
	}
	
	if len(errors) > 0 {
		os.Exit(1)
	}
}

func validatePod(node *yaml.Node, filename string) []string {
	var errors []string
	fields := parseMapping(node)

	// apiVersion
	if apiVersion, exists := fields["apiVersion"]; !exists {
		errors = append(errors, "apiVersion is required")
	} else if apiVersion.Kind != yaml.ScalarNode || apiVersion.Value != "v1" {
		errors = append(errors, fmt.Sprintf("%s:%d apiVersion has unsupported value '%s'", filename, apiVersion.Line, apiVersion.Value))
	}

	// kind
	if kind, exists := fields["kind"]; !exists {
		errors = append(errors, "kind is required")
	} else if kind.Kind != yaml.ScalarNode || kind.Value != "Pod" {
		errors = append(errors, fmt.Sprintf("%s:%d kind has unsupported value '%s'", filename, kind.Line, kind.Value))
	}

	// metadata
	if metadata, exists := fields["metadata"]; !exists {
		errors = append(errors, "metadata is required")
	} else {
		errors = append(errors, validateMetadata(metadata, filename)...)
	}

	// spec
	if spec, exists := fields["spec"]; !exists {
		errors = append(errors, "spec is required")
	} else {
		errors = append(errors, validateSpec(spec, filename)...)
	}

	return errors
}

func validateMetadata(node *yaml.Node, filename string) []string {
	var errors []string
	if node.Kind != yaml.MappingNode {
		errors = append(errors, fmt.Sprintf("%s:%d metadata must be object", filename, node.Line))
		return errors
	}

	fields := parseMapping(node)

	// name
	if name, exists := fields["name"]; !exists {
		errors = append(errors, "name is required")
	} else if name.Kind != yaml.ScalarNode {
		errors = append(errors, fmt.Sprintf("%s:%d name must be string", filename, name.Line))
	} else if name.Value == "" {
		errors = append(errors, fmt.Sprintf("%s:%d name is required", filename, name.Line))
	}

	// namespace (optional)
	if namespace, exists := fields["namespace"]; exists && namespace.Kind != yaml.ScalarNode {
		errors = append(errors, fmt.Sprintf("%s:%d namespace must be string", filename, namespace.Line))
	}

	// labels (optional)
	if labels, exists := fields["labels"]; exists && labels.Kind != yaml.MappingNode {
		errors = append(errors, fmt.Sprintf("%s:%d labels must be object", filename, labels.Line))
	}

	return errors
}

func validateSpec(node *yaml.Node, filename string) []string {
	var errors []string
	if node.Kind != yaml.MappingNode {
		errors = append(errors, fmt.Sprintf("%s:%d spec must be object", filename, node.Line))
		return errors
	}

	fields := parseMapping(node)

	// os (optional)
	if os, exists := fields["os"]; exists {
		errors = append(errors, validatePodOS(os, filename)...)
	}

	// containers
	if containers, exists := fields["containers"]; !exists {
		errors = append(errors, "containers is required")
	} else {
		errors = append(errors, validateContainers(containers, filename)...)
	}

	return errors
}

func validatePodOS(node *yaml.Node, filename string) []string {
	var errors []string
	
	if node.Kind == yaml.ScalarNode {
		if node.Value != "linux" && node.Value != "windows" {
			errors = append(errors, fmt.Sprintf("%s:%d os has unsupported value '%s'", filename, node.Line, node.Value))
		}
	} else if node.Kind == yaml.MappingNode {
		fields := parseMapping(node)
		if name, exists := fields["name"]; !exists {
			errors = append(errors, "name is required")
		} else if name.Kind != yaml.ScalarNode {
			errors = append(errors, fmt.Sprintf("%s:%d name must be string", filename, name.Line))
		} else if name.Value != "linux" && name.Value != "windows" {
			errors = append(errors, fmt.Sprintf("%s:%d name has unsupported value '%s'", filename, name.Line, name.Value))
		}
	} else {
		errors = append(errors, fmt.Sprintf("%s:%d os must be string or object", filename, node.Line))
	}

	return errors
}

func validateContainers(node *yaml.Node, filename string) []string {
	var errors []string
	if node.Kind != yaml.SequenceNode {
		errors = append(errors, fmt.Sprintf("%s:%d containers must be array", filename, node.Line))
		return errors
	}

	containerNames := make(map[string]int)
	for _, container := range node.Content {
		errs := validateContainer(container, filename)
		errors = append(errors, errs...)

		fields := parseMapping(container)
		if name, exists := fields["name"]; exists && name.Kind == yaml.ScalarNode {
			if prevLine, duplicate := containerNames[name.Value]; duplicate {
				errors = append(errors, fmt.Sprintf("%s:%d duplicate container name '%s' (first defined at line %d)", filename, name.Line, name.Value, prevLine))
			} else {
				containerNames[name.Value] = name.Line
			}
		}
	}

	return errors
}

func validateContainer(node *yaml.Node, filename string) []string {
	var errors []string
	if node.Kind != yaml.MappingNode {
		errors = append(errors, fmt.Sprintf("%s:%d container must be object", filename, node.Line))
		return errors
	}

	fields := parseMapping(node)

	// name
	if name, exists := fields["name"]; !exists {
		errors = append(errors, "name is required")
	} else if name.Kind != yaml.ScalarNode {
		errors = append(errors, fmt.Sprintf("%s:%d name must be string", filename, name.Line))
	} else if name.Value == "" {
		errors = append(errors, fmt.Sprintf("%s:%d name is required", filename, name.Line))
	} else if !snakeCaseRegex.MatchString(name.Value) {
		errors = append(errors, fmt.Sprintf("%s:%d name has invalid format '%s'", filename, name.Line, name.Value))
	}

	// image
	if image, exists := fields["image"]; !exists {
		errors = append(errors, "image is required")
	} else if image.Kind != yaml.ScalarNode {
		errors = append(errors, fmt.Sprintf("%s:%d image must be string", filename, image.Line))
	} else {
		if !strings.HasPrefix(image.Value, "registry.bigbrother.io/") {
			errors = append(errors, fmt.Sprintf("%s:%d image has invalid format '%s'", filename, image.Line, image.Value))
		} else if !strings.Contains(image.Value, ":") || strings.HasSuffix(image.Value, ":") {
			errors = append(errors, fmt.Sprintf("%s:%d image has invalid format '%s'", filename, image.Line, image.Value))
		}
	}

	// ports (optional)
	if ports, exists := fields["ports"]; exists {
		errors = append(errors, validateContainerPorts(ports, filename)...)
	}

	// readinessProbe (optional)
	if probe, exists := fields["readinessProbe"]; exists {
		errors = append(errors, validateProbe(probe, filename)...)
	}

	// livenessProbe (optional)
	if probe, exists := fields["livenessProbe"]; exists {
		errors = append(errors, validateProbe(probe, filename)...)
	}

	// resources
	if resources, exists := fields["resources"]; !exists {
		errors = append(errors, "resources is required")
	} else {
		errors = append(errors, validateResources(resources, filename)...)
	}

	return errors
}

func validateContainerPorts(node *yaml.Node, filename string) []string {
	var errors []string
	if node.Kind != yaml.SequenceNode {
		errors = append(errors, fmt.Sprintf("%s:%d ports must be array", filename, node.Line))
		return errors
	}

	for _, port := range node.Content {
		errors = append(errors, validateContainerPort(port, filename)...)
	}

	return errors
}

func validateContainerPort(node *yaml.Node, filename string) []string {
	var errors []string
	if node.Kind != yaml.MappingNode {
		errors = append(errors, fmt.Sprintf("%s:%d port must be object", filename, node.Line))
		return errors
	}

	fields := parseMapping(node)

	// containerPort
	if containerPort, exists := fields["containerPort"]; !exists {
		errors = append(errors, "containerPort is required")
	} else if containerPort.Kind != yaml.ScalarNode {
		errors = append(errors, fmt.Sprintf("%s:%d containerPort must be int", filename, containerPort.Line))
	} else {
		port, err := strconv.Atoi(containerPort.Value)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s:%d containerPort must be int", filename, containerPort.Line))
		} else if port <= 0 || port >= 65536 {
			errors = append(errors, fmt.Sprintf("%s:%d containerPort value out of range", filename, containerPort.Line))
		}
	}

	// protocol (optional)
	if protocol, exists := fields["protocol"]; exists {
		if protocol.Kind != yaml.ScalarNode {
			errors = append(errors, fmt.Sprintf("%s:%d protocol must be string", filename, protocol.Line))
		} else if protocol.Value != "TCP" && protocol.Value != "UDP" {
			errors = append(errors, fmt.Sprintf("%s:%d protocol has unsupported value '%s'", filename, protocol.Line, protocol.Value))
		}
	}

	return errors
}

func validateProbe(node *yaml.Node, filename string) []string {
	var errors []string
	if node.Kind != yaml.MappingNode {
		errors = append(errors, fmt.Sprintf("%s:%d probe must be object", filename, node.Line))
		return errors
	}

	fields := parseMapping(node)

	// httpGet
	if httpGet, exists := fields["httpGet"]; !exists {
		errors = append(errors, "httpGet is required")
	} else {
		errors = append(errors, validateHTTPGetAction(httpGet, filename)...)
	}

	return errors
}

func validateHTTPGetAction(node *yaml.Node, filename string) []string {
	var errors []string
	if node.Kind != yaml.MappingNode {
		errors = append(errors, fmt.Sprintf("%s:%d httpGet must be object", filename, node.Line))
		return errors
	}

	fields := parseMapping(node)

	// path
	if path, exists := fields["path"]; !exists {
		errors = append(errors, "path is required")
	} else if path.Kind != yaml.ScalarNode {
		errors = append(errors, fmt.Sprintf("%s:%d path must be string", filename, path.Line))
	} else if !strings.HasPrefix(path.Value, "/") {
		errors = append(errors, fmt.Sprintf("%s:%d path has invalid format '%s'", filename, path.Line, path.Value))
	}

	// port
	if port, exists := fields["port"]; !exists {
		errors = append(errors, "port is required")
	} else if port.Kind != yaml.ScalarNode {
		errors = append(errors, fmt.Sprintf("%s:%d port must be int", filename, port.Line))
	} else {
		portNum, err := strconv.Atoi(port.Value)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s:%d port must be int", filename, port.Line))
		} else if portNum <= 0 || portNum >= 65536 {
			errors = append(errors, fmt.Sprintf("%s:%d port value out of range", filename, port.Line))
		}
	}

	return errors
}

func validateResources(node *yaml.Node, filename string) []string {
	var errors []string
	if node.Kind != yaml.MappingNode {
		errors = append(errors, fmt.Sprintf("%s:%d resources must be object", filename, node.Line))
		return errors
	}

	fields := parseMapping(node)

	// requests (optional)
	if requests, exists := fields["requests"]; exists {
		errors = append(errors, validateResourceSpec(requests, filename, "requests")...)
	}

	// limits (optional)
	if limits, exists := fields["limits"]; exists {
		errors = append(errors, validateResourceSpec(limits, filename, "limits")...)
	}

	return errors
}

func validateResourceSpec(node *yaml.Node, filename string, fieldName string) []string {
	var errors []string
	if node.Kind != yaml.MappingNode {
		errors = append(errors, fmt.Sprintf("%s:%d %s must be object", filename, node.Line, fieldName))
		return errors
	}

	fields := parseMapping(node)

	// cpu (optional) - must be integer type, not string
	if cpu, exists := fields["cpu"]; exists {
		if cpu.Kind != yaml.ScalarNode {
			errors = append(errors, fmt.Sprintf("%s:%d cpu must be int", filename, cpu.Line))
		} else {
			// Check if it's explicitly a string in YAML (quoted)
			if strings.Contains(cpu.Tag, "str") {
				errors = append(errors, fmt.Sprintf("%s:%d cpu must be int", filename, cpu.Line))
			} else {
				_, err := strconv.Atoi(cpu.Value)
				if err != nil {
					errors = append(errors, fmt.Sprintf("%s:%d cpu must be int", filename, cpu.Line))
				}
			}
		}
	}

	// memory (optional)
	if memory, exists := fields["memory"]; exists {
		if memory.Kind != yaml.ScalarNode {
			errors = append(errors, fmt.Sprintf("%s:%d memory must be string", filename, memory.Line))
		} else if !memoryRegex.MatchString(memory.Value) {
			errors = append(errors, fmt.Sprintf("%s:%d memory has invalid format '%s'", filename, memory.Line, memory.Value))
		}
	}

	return errors
}

func parseMapping(node *yaml.Node) map[string]*yaml.Node {
	fields := make(map[string]*yaml.Node)
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i].Value
		value := node.Content[i+1]
		fields[key] = value
	}
	return fields
}