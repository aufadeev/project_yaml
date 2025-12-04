package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type ValidationError struct {
	File string
	Line int
	Msg  string
}

func (e *ValidationError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("%s:%d %s", e.File, e.Line, e.Msg)
	}
	return fmt.Sprintf("%s %s", e.File, e.Msg)
}

func findNodeByKey(parent *yaml.Node, key string) *yaml.Node {
	if parent.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			return parent.Content[i+1]
		}
	}
	return nil
}

func requireField(parent *yaml.Node, key string) (*yaml.Node, error) {
	node := findNodeByKey(parent, key)
	if node == nil {
		return nil, fmt.Errorf("%s is required", key)
	}
	return node, nil
}

// Принимаем любой scalar как строку (включая пустую)
func validateString(node *yaml.Node, field string) error {
	if node.Kind == yaml.ScalarNode {
		return nil
	}
	return fmt.Errorf("%s must be string", field)
}

// Принимаем любой scalar, который можно распарсить как int (включая строки "123")
func validateInt(node *yaml.Node, field string) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("%s must be int", field)
	}
	if _, err := strconv.Atoi(node.Value); err != nil {
		return fmt.Errorf("%s must be int", field)
	}
	return nil
}

func validateObjectMeta(node *yaml.Node, file string) error {
	if node.Kind != yaml.MappingNode {
		return &ValidationError{File: file, Line: node.Line, Msg: "metadata must be object"}
	}

	nameNode, err := requireField(node, "name")
	if err != nil {
		return &ValidationError{File: file, Msg: "metadata.name is required"}
	}
	if err := validateString(nameNode, "metadata.name"); err != nil {
		return &ValidationError{File: file, Line: nameNode.Line, Msg: err.Error()}
	}

	if nsNode := findNodeByKey(node, "namespace"); nsNode != nil {
		if err := validateString(nsNode, "metadata.namespace"); err != nil {
			return &ValidationError{File: file, Line: nsNode.Line, Msg: err.Error()}
		}
	}

	if labelsNode := findNodeByKey(node, "labels"); labelsNode != nil {
		if labelsNode.Kind != yaml.MappingNode {
			return &ValidationError{File: file, Line: labelsNode.Line, Msg: "metadata.labels must be object"}
		}
		for i := 0; i < len(labelsNode.Content); i += 2 {
			valNode := labelsNode.Content[i+1]
			if err := validateString(valNode, "metadata.labels value"); err != nil {
				return &ValidationError{File: file, Line: valNode.Line, Msg: "metadata.labels value must be string"}
			}
		}
	}

	return nil
}

func validatePodOS(node *yaml.Node, file string) error {
	if node.Kind != yaml.MappingNode {
		return &ValidationError{File: file, Line: node.Line, Msg: "os must be object"}
	}
	nameNode, err := requireField(node, "name")
	if err != nil {
		return &ValidationError{File: file, Msg: "spec.os.name is required"}
	}
	if err := validateString(nameNode, "spec.os.name"); err != nil {
		return &ValidationError{File: file, Line: nameNode.Line, Msg: err.Error()}
	}
	return nil
}

func validateHTTPGetAction(node *yaml.Node, prefix, file string) error {
	if node.Kind != yaml.MappingNode {
		return &ValidationError{File: file, Line: node.Line, Msg: prefix + ".httpGet must be object"}
	}

	pathNode, err := requireField(node, "path")
	if err != nil {
		return &ValidationError{File: file, Msg: prefix + ".path is required"}
	}
	if err := validateString(pathNode, prefix+".path"); err != nil {
		return &ValidationError{File: file, Line: pathNode.Line, Msg: err.Error()}
	}
	if !strings.HasPrefix(pathNode.Value, "/") {
		return &ValidationError{File: file, Line: pathNode.Line, Msg: fmt.Sprintf("%s.path has invalid format '%s'", prefix, pathNode.Value)}
	}

	portNode, err := requireField(node, "port")
	if err != nil {
		return &ValidationError{File: file, Msg: prefix + ".port is required"}
	}
	if err := validateInt(portNode, prefix+".port"); err != nil {
		return &ValidationError{File: file, Line: portNode.Line, Msg: err.Error()}
	}

	return nil
}

func validateProbe(node *yaml.Node, prefix, file string) error {
	if node.Kind != yaml.MappingNode {
		return &ValidationError{File: file, Line: node.Line, Msg: prefix + " must be object"}
	}
	httpGetNode, err := requireField(node, "httpGet")
	if err != nil {
		return &ValidationError{File: file, Msg: prefix + ".httpGet is required"}
	}
	if err := validateHTTPGetAction(httpGetNode, prefix+".httpGet", file); err != nil {
		return err
	}
	return nil
}

func validateResourceRequirements(node *yaml.Node, prefix, file string) error {
	if node.Kind != yaml.MappingNode {
		return &ValidationError{File: file, Line: node.Line, Msg: prefix + " must be object"}
	}

	checkResource := func(parent *yaml.Node, kind string) error {
		if parent == nil {
			return nil
		}
		if parent.Kind != yaml.MappingNode {
			return &ValidationError{File: file, Line: parent.Line, Msg: fmt.Sprintf("%s.%s must be object", prefix, kind)}
		}
		for i := 0; i < len(parent.Content); i += 2 {
			valNode := parent.Content[i+1]
			key := parent.Content[i].Value
			switch key {
			case "cpu":
				if err := validateInt(valNode, fmt.Sprintf("%s.%s.cpu", prefix, kind)); err != nil {
					return &ValidationError{File: file, Line: valNode.Line, Msg: err.Error()}
				}
			case "memory":
				if err := validateString(valNode, fmt.Sprintf("%s.%s.memory", prefix, kind)); err != nil {
					return &ValidationError{File: file, Line: valNode.Line, Msg: err.Error()}
				}
			}
		}
		return nil
	}

	if limitsNode := findNodeByKey(node, "limits"); limitsNode != nil {
		if err := checkResource(limitsNode, "limits"); err != nil {
			return err
		}
	}
	if requestsNode := findNodeByKey(node, "requests"); requestsNode != nil {
		if err := checkResource(requestsNode, "requests"); err != nil {
			return err
		}
	}

	return nil
}

func validateContainerPort(node *yaml.Node, file string) error {
	if node.Kind != yaml.MappingNode {
		return &ValidationError{File: file, Line: node.Line, Msg: "ports item must be object"}
	}

	containerPortNode, err := requireField(node, "containerPort")
	if err != nil {
		return &ValidationError{File: file, Msg: "containerPort is required"}
	}
	if err := validateInt(containerPortNode, "containerPort"); err != nil {
		return &ValidationError{File: file, Line: containerPortNode.Line, Msg: err.Error()}
	}

	return nil
}

func validateContainer(node *yaml.Node, file string, index int) error {
	if node.Kind != yaml.MappingNode {
		return &ValidationError{File: file, Line: node.Line, Msg: fmt.Sprintf("containers[%d] must be object", index)}
	}

	nameNode, err := requireField(node, "name")
	if err != nil {
		return &ValidationError{File: file, Msg: fmt.Sprintf("containers[%d].name is required", index)}
	}
	if err := validateString(nameNode, fmt.Sprintf("containers[%d].name", index)); err != nil {
		return &ValidationError{File: file, Line: nameNode.Line, Msg: err.Error()}
	}

	imageNode, err := requireField(node, "image")
	if err != nil {
		return &ValidationError{File: file, Msg: fmt.Sprintf("containers[%d].image is required", index)}
	}
	if err := validateString(imageNode, fmt.Sprintf("containers[%d].image", index)); err != nil {
		return &ValidationError{File: file, Line: imageNode.Line, Msg: err.Error()}
	}
	if !strings.Contains(imageNode.Value, ":") {
		return &ValidationError{File: file, Line: imageNode.Line, Msg: fmt.Sprintf("containers[%d].image has invalid format '%s'", index, imageNode.Value)}
	}

	if portsNode := findNodeByKey(node, "ports"); portsNode != nil {
		if portsNode.Kind != yaml.SequenceNode {
			return &ValidationError{File: file, Line: portsNode.Line, Msg: "containers[].ports must be array"}
		}
		for _, portItem := range portsNode.Content {
			if err := validateContainerPort(portItem, file); err != nil {
				return err
			}
		}
	}

	if rpNode := findNodeByKey(node, "readinessProbe"); rpNode != nil {
		if err := validateProbe(rpNode, fmt.Sprintf("containers[%d].readinessProbe", index), file); err != nil {
			return err
		}
	}

	if lpNode := findNodeByKey(node, "livenessProbe"); lpNode != nil {
		if err := validateProbe(lpNode, fmt.Sprintf("containers[%d].livenessProbe", index), file); err != nil {
			return err
		}
	}

	resourcesNode, err := requireField(node, "resources")
	if err != nil {
		return &ValidationError{File: file, Msg: fmt.Sprintf("containers[%d].resources is required", index)}
	}
	if err := validateResourceRequirements(resourcesNode, fmt.Sprintf("containers[%d].resources", index), file); err != nil {
		return err
	}

	return nil
}

func validateContainers(node *yaml.Node, file string) error {
	if node.Kind != yaml.SequenceNode {
		return &ValidationError{File: file, Line: node.Line, Msg: "spec.containers must be array"}
	}
	if len(node.Content) == 0 {
		return &ValidationError{File: file, Msg: "spec.containers is required"}
	}
	for i, container := range node.Content {
		if err := validateContainer(container, file, i); err != nil {
			return err
		}
	}
	return nil
}

func validatePodSpec(node *yaml.Node, file string) error {
	if node.Kind != yaml.MappingNode {
		return &ValidationError{File: file, Line: node.Line, Msg: "spec must be object"}
	}

	if osNode := findNodeByKey(node, "os"); osNode != nil {
		if err := validatePodOS(osNode, file); err != nil {
			return err
		}
	}

	containersNode, err := requireField(node, "containers")
	if err != nil {
		return &ValidationError{File: file, Msg: "spec.containers is required"}
	}
	if err := validateContainers(containersNode, file); err != nil {
		return err
	}

	return nil
}

func validateTopLevel(node *yaml.Node, file string) error {
	if node.Kind != yaml.DocumentNode {
		return &ValidationError{File: file, Msg: "expected document node"}
	}
	if len(node.Content) == 0 {
		return &ValidationError{File: file, Msg: "empty document"}
	}
	doc := node.Content[0]
	if doc.Kind != yaml.MappingNode {
		return &ValidationError{File: file, Line: doc.Line, Msg: "root must be object"}
	}

	apiVersionNode, err := requireField(doc, "apiVersion")
	if err != nil {
		return &ValidationError{File: file, Msg: "apiVersion is required"}
	}
	if err := validateString(apiVersionNode, "apiVersion"); err != nil {
		return &ValidationError{File: file, Line: apiVersionNode.Line, Msg: err.Error()}
	}
	if apiVersionNode.Value != "v1" {
		return &ValidationError{File: file, Line: apiVersionNode.Line, Msg: fmt.Sprintf("apiVersion has unsupported value '%s'", apiVersionNode.Value)}
	}

	kindNode, err := requireField(doc, "kind")
	if err != nil {
		return &ValidationError{File: file, Msg: "kind is required"}
	}
	if err := validateString(kindNode, "kind"); err != nil {
		return &ValidationError{File: file, Line: kindNode.Line, Msg: err.Error()}
	}
	if kindNode.Value != "Pod" {
		return &ValidationError{File: file, Line: kindNode.Line, Msg: fmt.Sprintf("kind has unsupported value '%s'", kindNode.Value)}
	}

	metadataNode, err := requireField(doc, "metadata")
	if err != nil {
		return &ValidationError{File: file, Msg: "metadata is required"}
	}
	if err := validateObjectMeta(metadataNode, file); err != nil {
		return err
	}

	specNode, err := requireField(doc, "spec")
	if err != nil {
		return &ValidationError{File: file, Msg: "spec is required"}
	}
	if err := validatePodSpec(specNode, file); err != nil {
		return err
	}

	return nil
}

func validateFile(path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("cannot resolve absolute path: %w", err)
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("cannot read file: %w", err)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		return &ValidationError{File: path, Msg: "cannot unmarshal YAML"}
	}

	if len(root.Content) == 0 {
		return &ValidationError{File: path, Msg: "empty YAML"}
	}

	for _, docNode := range root.Content {
		if err := validateTopLevel(docNode, path); err != nil {
			return err
		}
	}

	return nil
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <yaml-file>\n", os.Args[0])
		os.Exit(1)
	}

	filePath := os.Args[1]
	if err := validateFile(filePath); err != nil {
		if ve, ok := err.(*ValidationError); ok {
			fmt.Fprintln(os.Stderr, ve.Error())
		} else {
			fmt.Fprintf(os.Stderr, "%s %s\n", filePath, err.Error())
		}
		os.Exit(1)
	}

	os.Exit(0)
}