package main

import (
	"fmt"
	"os"
	//"path/filepath"
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
	if node := findNodeByKey(parent, key); node != nil {
		return node, nil
	}
	return nil, fmt.Errorf("%s is required", key)
}

func validateString(node *yaml.Node, field string) error {
	if node.Kind == yaml.ScalarNode && (node.Tag == "!!str" || node.Tag == "!!null") {
		return nil
	}
	return fmt.Errorf("%s must be string", field)
}

func validateInt(node *yaml.Node, field string) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("%s must be int", field)
	}
	switch node.Tag {
	case "!!int":
		_, err := strconv.Atoi(node.Value)
		if err != nil {
			return fmt.Errorf("%s must be int", field)
		}
	case "!!str":
		_, err := strconv.Atoi(node.Value)
		if err != nil {
			return fmt.Errorf("%s must be int", field)
		}
	default:
		return fmt.Errorf("%s must be int", field)
	}
	return nil
}

// Просто проверяем, что os.name — строка. Значение не проверяем.
func validatePodOS(node *yaml.Node, file string) error {
	if node.Kind != yaml.MappingNode {
		return &ValidationError{File: file, Line: node.Line, Msg: "os must be object"}
	}
	nameNode, err := requireField(node, "name")
	if err != nil {
		return &ValidationError{File: file, Msg: err.Error()}
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
		return &ValidationError{File: file, Msg: prefix + "." + err.Error()}
	}
	if err := validateString(pathNode, prefix+".path"); err != nil {
		return &ValidationError{File: file, Line: pathNode.Line, Msg: err.Error()}
	}
	// Проверка, что путь начинается с / — ОСТАВЛЕНА, так как в примерах она есть
	if !strings.HasPrefix(pathNode.Value, "/") {
		return &ValidationError{File: file, Line: pathNode.Line, Msg: fmt.Sprintf("%s.path has invalid format '%s'", prefix, pathNode.Value)}
	}

	portNode, err := requireField(node, "port")
	if err != nil {
		return &ValidationError{File: file, Msg: prefix + "." + err.Error()}
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
	httpGet, err := requireField(node, "httpGet")
	if err != nil {
		return &ValidationError{File: file, Msg: prefix + "." + err.Error()}
	}
	return validateHTTPGetAction(httpGet, prefix+".httpGet", file)
}

func validateResource(node *yaml.Node, prefix, file string) error {
	if node.Kind != yaml.MappingNode {
		return &ValidationError{File: file, Line: node.Line, Msg: prefix + " must be object"}
	}
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i].Value
		val := node.Content[i+1]
		switch key {
		case "cpu":
			if err := validateInt(val, prefix+".cpu"); err != nil {
				return &ValidationError{File: file, Line: val.Line, Msg: err.Error()}
			}
		case "memory":
			if err := validateString(val, prefix+".memory"); err != nil {
				return &ValidationError{File: file, Line: val.Line, Msg: err.Error()}
			}
		}
	}
	return nil
}

func validateResourceRequirements(node *yaml.Node, prefix, file string) error {
	if node.Kind != yaml.MappingNode {
		return &ValidationError{File: file, Line: node.Line, Msg: prefix + " must be object"}
	}
	if limits := findNodeByKey(node, "limits"); limits != nil {
		if err := validateResource(limits, prefix+".limits", file); err != nil {
			return err
		}
	}
	if requests := findNodeByKey(node, "requests"); requests != nil {
		if err := validateResource(requests, prefix+".requests", file); err != nil {
			return err
		}
	}
	return nil
}

func validateContainerPort(node *yaml.Node, file string) error {
	if node.Kind != yaml.MappingNode {
		return &ValidationError{File: file, Line: node.Line, Msg: "ports item must be object"}
	}
	containerPort, err := requireField(node, "containerPort")
	if err != nil {
		return &ValidationError{File: file, Msg: "containerPort is required"}
	}
	if err := validateInt(containerPort, "containerPort"); err != nil {
		return &ValidationError{File: file, Line: containerPort.Line, Msg: err.Error()}
	}
	return nil
}

func validateContainer(node *yaml.Node, file string, idx int) error {
	if node.Kind != yaml.MappingNode {
		return &ValidationError{File: file, Line: node.Line, Msg: fmt.Sprintf("containers[%d] must be object", idx)}
	}

	if name, err := requireField(node, "name"); err != nil {
		return &ValidationError{File: file, Msg: fmt.Sprintf("containers[%d].name is required", idx)}
	} else if err := validateString(name, fmt.Sprintf("containers[%d].name", idx)); err != nil {
		return &ValidationError{File: file, Line: name.Line, Msg: err.Error()}
	}

	if image, err := requireField(node, "image"); err != nil {
		return &ValidationError{File: file, Msg: fmt.Sprintf("containers[%d].image is required", idx)}
	} else if err := validateString(image, fmt.Sprintf("containers[%d].image", idx)); err != nil {
		return &ValidationError{File: file, Line: image.Line, Msg: err.Error()}
	}

	if ports := findNodeByKey(node, "ports"); ports != nil {
		if ports.Kind != yaml.SequenceNode {
			return &ValidationError{File: file, Line: ports.Line, Msg: "containers[].ports must be array"}
		}
		for _, p := range ports.Content {
			if err := validateContainerPort(p, file); err != nil {
				return err
			}
		}
	}

	if rp := findNodeByKey(node, "readinessProbe"); rp != nil {
		if err := validateProbe(rp, fmt.Sprintf("containers[%d].readinessProbe", idx), file); err != nil {
			return err
		}
	}
	if lp := findNodeByKey(node, "livenessProbe"); lp != nil {
		if err := validateProbe(lp, fmt.Sprintf("containers[%d].livenessProbe", idx), file); err != nil {
			return err
		}
	}

	resources, err := requireField(node, "resources")
	if err != nil {
		return &ValidationError{File: file, Msg: fmt.Sprintf("containers[%d].resources is required", idx)}
	}
	if err := validateResourceRequirements(resources, fmt.Sprintf("containers[%d].resources", idx), file); err != nil {
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
	for i, c := range node.Content {
		if err := validateContainer(c, file, i); err != nil {
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
	containers, err := requireField(node, "containers")
	if err != nil {
		return &ValidationError{File: file, Msg: "spec.containers is required"}
	}
	return validateContainers(containers, file)
}

func validateObjectMeta(node *yaml.Node, file string) error {
	if node.Kind != yaml.MappingNode {
		return &ValidationError{File: file, Line: node.Line, Msg: "metadata must be object"}
	}
	name, err := requireField(node, "name")
	if err != nil {
		return &ValidationError{File: file, Msg: "metadata.name is required"}
	}
	if err := validateString(name, "metadata.name"); err != nil {
		return &ValidationError{File: file, Line: name.Line, Msg: err.Error()}
	}
	return nil
}

func validateTopLevel(node *yaml.Node, file string) error {
	if node.Kind != yaml.DocumentNode || len(node.Content) == 0 {
		return &ValidationError{File: file, Msg: "invalid document"}
	}
	doc := node.Content[0]
	if doc.Kind != yaml.MappingNode {
		return &ValidationError{File: file, Line: doc.Line, Msg: "root must be object"}
	}

	apiVersion, err := requireField(doc, "apiVersion")
	if err != nil {
		return &ValidationError{File: file, Msg: "apiVersion is required"}
	}
	if err := validateString(apiVersion, "apiVersion"); err != nil {
		return &ValidationError{File: file, Line: apiVersion.Line, Msg: err.Error()}
	}
	if apiVersion.Value != "v1" {
		return &ValidationError{File: file, Line: apiVersion.Line, Msg: fmt.Sprintf("apiVersion has unsupported value '%s'", apiVersion.Value)}
	}

	kind, err := requireField(doc, "kind")
	if err != nil {
		return &ValidationError{File: file, Msg: "kind is required"}
	}
	if err := validateString(kind, "kind"); err != nil {
		return &ValidationError{File: file, Line: kind.Line, Msg: err.Error()}
	}
	if kind.Value != "Pod" {
		return &ValidationError{File: file, Line: kind.Line, Msg: fmt.Sprintf("kind has unsupported value '%s'", kind.Value)}
	}

	metadata, err := requireField(doc, "metadata")
	if err != nil {
		return &ValidationError{File: file, Msg: "metadata is required"}
	}
	if err := validateObjectMeta(metadata, file); err != nil {
		return err
	}

	spec, err := requireField(doc, "spec")
	if err != nil {
		return &ValidationError{File: file, Msg: "spec is required"}
	}
	if err := validatePodSpec(spec, file); err != nil {
		return err
	}

	return nil
}

func validateFile(path string) error {
	content, err := os.ReadFile(path)
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
	for _, doc := range root.Content {
		if err := validateTopLevel(doc, path); err != nil {
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
	if err := validateFile(os.Args[1]); err != nil {
		if ve, ok := err.(*ValidationError); ok {
			fmt.Fprintln(os.Stderr, ve.Error())
		} else {
			fmt.Fprintf(os.Stderr, "%s %s\n", os.Args[1], err.Error())
		}
		os.Exit(1)
	}
}