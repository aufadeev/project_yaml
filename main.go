package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type ValidationError struct {
	Line    int
	Message string
}

func (e ValidationError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf(":%d %s", e.Line, e.Message)
	}
	return e.Message
}

var snakeCaseRegex = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
var memoryRegex = regexp.MustCompile(`^\d+(Gi|Mi|Ki)$`)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: yamlvalid <path-to-yaml-file>")
		os.Exit(1)
	}

	filename := os.Args[1]
	errors := validateFile(filename)

	if len(errors) > 0 {
		for _, err := range errors {
			if err.Line > 0 {
				fmt.Fprintf(os.Stderr, "%s:%d %s\n", filename, err.Line, err.Message)
			} else {
				fmt.Fprintf(os.Stderr, "%s\n", err.Message)
			}
		}
		os.Exit(1)
	}

	os.Exit(0)
}

func validateFile(filename string) []ValidationError {
	var errors []ValidationError

	content, err := os.ReadFile(filename)
	if err != nil {
		errors = append(errors, ValidationError{Message: fmt.Sprintf("cannot read file: %v", err)})
		return errors
	}

	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		errors = append(errors, ValidationError{Message: fmt.Sprintf("cannot unmarshal YAML: %v", err)})
		return errors
	}

	if len(root.Content) == 0 {
		errors = append(errors, ValidationError{Message: "empty YAML file"})
		return errors
	}

	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		errors = append(errors, ValidationError{Line: doc.Line, Message: "root must be a mapping"})
		return errors
	}

	errors = append(errors, validatePod(doc)...)
	return errors
}

func validatePod(node *yaml.Node) []ValidationError {
	var errors []ValidationError
	fields := parseMapping(node)

	// apiVersion
	if apiVersion, exists := fields["apiVersion"]; !exists {
		errors = append(errors, ValidationError{Message: "apiVersion is required"})
	} else if apiVersion.Kind != yaml.ScalarNode || apiVersion.Value != "v1" {
		errors = append(errors, ValidationError{Line: apiVersion.Line, Message: "apiVersion has unsupported value '" + apiVersion.Value + "'"})
	}

	// kind
	if kind, exists := fields["kind"]; !exists {
		errors = append(errors, ValidationError{Message: "kind is required"})
	} else if kind.Kind != yaml.ScalarNode || kind.Value != "Pod" {
		errors = append(errors, ValidationError{Line: kind.Line, Message: "kind has unsupported value '" + kind.Value + "'"})
	}

	// metadata
	if metadata, exists := fields["metadata"]; !exists {
		errors = append(errors, ValidationError{Message: "metadata is required"})
	} else {
		errors = append(errors, validateMetadata(metadata)...)
	}

	// spec
	if spec, exists := fields["spec"]; !exists {
		errors = append(errors, ValidationError{Message: "spec is required"})
	} else {
		errors = append(errors, validateSpec(spec)...)
	}

	return errors
}

func validateMetadata(node *yaml.Node) []ValidationError {
	var errors []ValidationError
	if node.Kind != yaml.MappingNode {
		errors = append(errors, ValidationError{Line: node.Line, Message: "metadata must be object"})
		return errors
	}

	fields := parseMapping(node)

	// name
	if name, exists := fields["name"]; !exists {
		errors = append(errors, ValidationError{Message: "name is required"})
	} else if name.Kind != yaml.ScalarNode {
		errors = append(errors, ValidationError{Line: name.Line, Message: "name must be string"})
	} else if name.Value == "" {
		errors = append(errors, ValidationError{Line: name.Line, Message: "name is required"})
	}

	// namespace (optional)
	if namespace, exists := fields["namespace"]; exists && namespace.Kind != yaml.ScalarNode {
		errors = append(errors, ValidationError{Line: namespace.Line, Message: "namespace must be string"})
	}

	// labels (optional)
	if labels, exists := fields["labels"]; exists && labels.Kind != yaml.MappingNode {
		errors = append(errors, ValidationError{Line: labels.Line, Message: "labels must be object"})
	}

	return errors
}

func validateSpec(node *yaml.Node) []ValidationError {
	var errors []ValidationError
	if node.Kind != yaml.MappingNode {
		errors = append(errors, ValidationError{Line: node.Line, Message: "spec must be object"})
		return errors
	}

	fields := parseMapping(node)

	// os (optional)
	if os, exists := fields["os"]; exists {
		errors = append(errors, validatePodOS(os)...)
	}

	// containers
	if containers, exists := fields["containers"]; !exists {
		errors = append(errors, ValidationError{Message: "containers is required"})
	} else {
		errors = append(errors, validateContainers(containers)...)
	}

	return errors
}

func validatePodOS(node *yaml.Node) []ValidationError {
	var errors []ValidationError
	
	if node.Kind == yaml.ScalarNode {
		// Simple string format
		if node.Value != "linux" && node.Value != "windows" {
			errors = append(errors, ValidationError{Line: node.Line, Message: "os has unsupported value '" + node.Value + "'"})
		}
	} else if node.Kind == yaml.MappingNode {
		// Object format with "name" field
		fields := parseMapping(node)
		if name, exists := fields["name"]; !exists {
			errors = append(errors, ValidationError{Message: "name is required"})
		} else if name.Kind != yaml.ScalarNode {
			errors = append(errors, ValidationError{Line: name.Line, Message: "name must be string"})
		} else if name.Value != "linux" && name.Value != "windows" {
			errors = append(errors, ValidationError{Line: name.Line, Message: "name has unsupported value '" + name.Value + "'"})
		}
	} else {
		errors = append(errors, ValidationError{Line: node.Line, Message: "os must be string or object"})
	}

	return errors
}

func validateContainers(node *yaml.Node) []ValidationError {
	var errors []ValidationError
	if node.Kind != yaml.SequenceNode {
		errors = append(errors, ValidationError{Line: node.Line, Message: "containers must be array"})
		return errors
	}

	containerNames := make(map[string]int)
	for _, container := range node.Content {
		errs := validateContainer(container)
		errors = append(errors, errs...)

		// Check for unique names
		fields := parseMapping(container)
		if name, exists := fields["name"]; exists && name.Kind == yaml.ScalarNode {
			if prevLine, duplicate := containerNames[name.Value]; duplicate {
				errors = append(errors, ValidationError{Line: name.Line, Message: fmt.Sprintf("duplicate container name '%s' (first defined at line %d)", name.Value, prevLine)})
			} else {
				containerNames[name.Value] = name.Line
			}
		}
	}

	return errors
}

func validateContainer(node *yaml.Node) []ValidationError {
	var errors []ValidationError
	if node.Kind != yaml.MappingNode {
		errors = append(errors, ValidationError{Line: node.Line, Message: "container must be object"})
		return errors
	}

	fields := parseMapping(node)

	// name
	if name, exists := fields["name"]; !exists {
		errors = append(errors, ValidationError{Message: "name is required"})
	} else if name.Kind != yaml.ScalarNode {
		errors = append(errors, ValidationError{Line: name.Line, Message: "name must be string"})
	} else if name.Value == "" {
		errors = append(errors, ValidationError{Line: name.Line, Message: "name is required"})
	} else if !snakeCaseRegex.MatchString(name.Value) {
		errors = append(errors, ValidationError{Line: name.Line, Message: "name has invalid format '" + name.Value + "'"})
	}

	// image
	if image, exists := fields["image"]; !exists {
		errors = append(errors, ValidationError{Message: "image is required"})
	} else if image.Kind != yaml.ScalarNode {
		errors = append(errors, ValidationError{Line: image.Line, Message: "image must be string"})
	} else {
		if !strings.HasPrefix(image.Value, "registry.bigbrother.io/") {
			errors = append(errors, ValidationError{Line: image.Line, Message: "image has invalid format '" + image.Value + "'"})
		} else if !strings.Contains(image.Value, ":") || strings.HasSuffix(image.Value, ":") {
			errors = append(errors, ValidationError{Line: image.Line, Message: "image has invalid format '" + image.Value + "'"})
		}
	}

	// ports (optional)
	if ports, exists := fields["ports"]; exists {
		errors = append(errors, validateContainerPorts(ports)...)
	}

	// readinessProbe (optional)
	if probe, exists := fields["readinessProbe"]; exists {
		errors = append(errors, validateProbe(probe)...)
	}

	// livenessProbe (optional)
	if probe, exists := fields["livenessProbe"]; exists {
		errors = append(errors, validateProbe(probe)...)
	}

	// resources
	if resources, exists := fields["resources"]; !exists {
		errors = append(errors, ValidationError{Message: "resources is required"})
	} else {
		errors = append(errors, validateResources(resources)...)
	}

	return errors
}

func validateContainerPorts(node *yaml.Node) []ValidationError {
	var errors []ValidationError
	if node.Kind != yaml.SequenceNode {
		errors = append(errors, ValidationError{Line: node.Line, Message: "ports must be array"})
		return errors
	}

	for _, port := range node.Content {
		errors = append(errors, validateContainerPort(port)...)
	}

	return errors
}

func validateContainerPort(node *yaml.Node) []ValidationError {
	var errors []ValidationError
	if node.Kind != yaml.MappingNode {
		errors = append(errors, ValidationError{Line: node.Line, Message: "port must be object"})
		return errors
	}

	fields := parseMapping(node)

	// containerPort
	if containerPort, exists := fields["containerPort"]; !exists {
		errors = append(errors, ValidationError{Message: "containerPort is required"})
	} else if containerPort.Kind != yaml.ScalarNode {
		errors = append(errors, ValidationError{Line: containerPort.Line, Message: "containerPort must be int"})
	} else {
		port, err := strconv.Atoi(containerPort.Value)
		if err != nil {
			errors = append(errors, ValidationError{Line: containerPort.Line, Message: "containerPort must be int"})
		} else if port <= 0 || port >= 65536 {
			errors = append(errors, ValidationError{Line: containerPort.Line, Message: "containerPort value out of range"})
		}
	}

	// protocol (optional)
	if protocol, exists := fields["protocol"]; exists {
		if protocol.Kind != yaml.ScalarNode {
			errors = append(errors, ValidationError{Line: protocol.Line, Message: "protocol must be string"})
		} else if protocol.Value != "TCP" && protocol.Value != "UDP" {
			errors = append(errors, ValidationError{Line: protocol.Line, Message: "protocol has unsupported value '" + protocol.Value + "'"})
		}
	}

	return errors
}

func validateProbe(node *yaml.Node) []ValidationError {
	var errors []ValidationError
	if node.Kind != yaml.MappingNode {
		errors = append(errors, ValidationError{Line: node.Line, Message: "probe must be object"})
		return errors
	}

	fields := parseMapping(node)

	// httpGet
	if httpGet, exists := fields["httpGet"]; !exists {
		errors = append(errors, ValidationError{Message: "httpGet is required"})
	} else {
		errors = append(errors, validateHTTPGetAction(httpGet)...)
	}

	return errors
}

func validateHTTPGetAction(node *yaml.Node) []ValidationError {
	var errors []ValidationError
	if node.Kind != yaml.MappingNode {
		errors = append(errors, ValidationError{Line: node.Line, Message: "httpGet must be object"})
		return errors
	}

	fields := parseMapping(node)

	// path
	if path, exists := fields["path"]; !exists {
		errors = append(errors, ValidationError{Message: "path is required"})
	} else if path.Kind != yaml.ScalarNode {
		errors = append(errors, ValidationError{Line: path.Line, Message: "path must be string"})
	} else if !strings.HasPrefix(path.Value, "/") {
		errors = append(errors, ValidationError{Line: path.Line, Message: "path has invalid format '" + path.Value + "'"})
	}

	// port
	if port, exists := fields["port"]; !exists {
		errors = append(errors, ValidationError{Message: "port is required"})
	} else if port.Kind != yaml.ScalarNode {
		errors = append(errors, ValidationError{Line: port.Line, Message: "port must be int"})
	} else {
		portNum, err := strconv.Atoi(port.Value)
		if err != nil {
			errors = append(errors, ValidationError{Line: port.Line, Message: "port must be int"})
		} else if portNum <= 0 || portNum >= 65536 {
			errors = append(errors, ValidationError{Line: port.Line, Message: "port value out of range"})
		}
	}

	return errors
}

func validateResources(node *yaml.Node) []ValidationError {
	var errors []ValidationError
	if node.Kind != yaml.MappingNode {
		errors = append(errors, ValidationError{Line: node.Line, Message: "resources must be object"})
		return errors
	}

	fields := parseMapping(node)

	// requests (optional)
	if requests, exists := fields["requests"]; exists {
		errors = append(errors, validateResourceSpec(requests, "requests")...)
	}

	// limits (optional)
	if limits, exists := fields["limits"]; exists {
		errors = append(errors, validateResourceSpec(limits, "limits")...)
	}

	return errors
}

func validateResourceSpec(node *yaml.Node, fieldName string) []ValidationError {
	var errors []ValidationError
	if node.Kind != yaml.MappingNode {
		errors = append(errors, ValidationError{Line: node.Line, Message: fieldName + " must be object"})
		return errors
	}

	fields := parseMapping(node)

	// cpu (optional) - can be int or string with int
	if cpu, exists := fields["cpu"]; exists {
		if cpu.Kind != yaml.ScalarNode {
			errors = append(errors, ValidationError{Line: cpu.Line, Message: "cpu must be int"})
		} else {
			// CPU must be an integer, not a quoted string
			// Check the tag to see if it was originally an integer
			if cpu.Tag == "!!str" {
				errors = append(errors, ValidationError{Line: cpu.Line, Message: "cpu must be int"})
			} else {
				_, err := strconv.Atoi(cpu.Value)
				if err != nil {
					errors = append(errors, ValidationError{Line: cpu.Line, Message: "cpu must be int"})
				}
			}
		}
	}

	// memory (optional)
	if memory, exists := fields["memory"]; exists {
		if memory.Kind != yaml.ScalarNode {
			errors = append(errors, ValidationError{Line: memory.Line, Message: "memory must be string"})
		} else if !memoryRegex.MatchString(memory.Value) {
			errors = append(errors, ValidationError{Line: memory.Line, Message: "memory has invalid format '" + memory.Value + "'"})
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