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

// mapNode преобразует YAML-мапу в map[string]*yaml.Node
func mapNode(n *yaml.Node) map[string]*yaml.Node {
	if n.Kind != yaml.MappingNode {
		return nil
	}
	m := make(map[string]*yaml.Node)
	for i := 0; i < len(n.Content); i += 2 {
		key := n.Content[i].Value
		val := n.Content[i+1]
		m[key] = val
	}
	return m
}

// getIntValue безопасно извлекает целое число из yaml.Node
func getIntValue(node *yaml.Node) (int, bool) {
	if node.Kind != yaml.ScalarNode {
		return 0, false
	}
	if node.Tag == "!!int" {
		if v, err := strconv.Atoi(node.Value); err == nil {
			return v, true
		}
	}
	// YAML может хранить число как строку
	if v, err := strconv.Atoi(node.Value); err == nil {
		return v, true
	}
	return 0, false
}

// isValidMemory проверяет формат памяти: \d+(Gi|Mi|Ki)
var memoryRegexp = regexp.MustCompile(`^\d+(Gi|Mi|Ki)$`)

func isValidMemory(s string) bool {
	return memoryRegexp.MatchString(s)
}

// isValidSnakeCase: [a-z][a-z0-9_]*, не пустое
func isValidSnakeCase(s string) bool {
	if s == "" {
		return false
	}
	match, _ := regexp.MatchString(`^[a-z][a-z0-9_]*$`, s)
	return match
}

// isValidImage: начинается с registry.bigbrother.io/, содержит тег
func isValidImage(s string) bool {
	if s == "" {
		return false
	}
	if !strings.HasPrefix(s, "registry.bigbrother.io/") {
		return false
	}
	return strings.Contains(s, ":")
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: yamlvalidator <file>\n")
		os.Exit(1)
	}

	filePath := os.Args[1]
	content, err := os.ReadFile(filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: cannot read file: %v\n", filepath.Base(filePath), err)
		os.Exit(1)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		fmt.Fprintf(os.Stderr, "%s: cannot unmarshal YAML: %v\n", filepath.Base(filePath), err)
		os.Exit(1)
	}

	if len(root.Content) == 0 {
		fmt.Fprintf(os.Stderr, "%s: empty YAML document\n", filepath.Base(filePath))
		os.Exit(1)
	}

	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		fmt.Fprintf(os.Stderr, "%s: root must be a mapping\n", filepath.Base(filePath))
		os.Exit(1)
	}

	errs := validateTop(filePath, doc)
	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, e)
		}
		os.Exit(1)
	}

	os.Exit(0)
}

func validateTop(filePath string, doc *yaml.Node) []string {
	fileBase := filepath.Base(filePath)
	var errs []string
	fields := mapNode(doc)

	// apiVersion
	if node, ok := fields["apiVersion"]; !ok {
		errs = append(errs, fmt.Sprintf("%s: apiVersion is required", fileBase))
	} else if node.Value != "v1" {
		errs = append(errs, fmt.Sprintf("%s:%d apiVersion has unsupported value '%s'", fileBase, node.Line, node.Value))
	}

	// kind
	if node, ok := fields["kind"]; !ok {
		errs = append(errs, fmt.Sprintf("%s: kind is required", fileBase))
	} else if node.Value != "Pod" {
		errs = append(errs, fmt.Sprintf("%s:%d kind has unsupported value '%s'", fileBase, node.Line, node.Value))
	}

	// metadata
	if metaNode, ok := fields["metadata"]; !ok {
		errs = append(errs, fmt.Sprintf("%s: metadata is required", fileBase))
	} else {
		meta := mapNode(metaNode)
		if nameNode, ok := meta["name"]; !ok || nameNode.Value == "" {
			errs = append(errs, fmt.Sprintf("%s: name is required", fileBase))
		}
		// namespace и labels — не обязательны
	}

	// spec
	if specNode, ok := fields["spec"]; !ok {
		errs = append(errs, fmt.Sprintf("%s: spec is required", fileBase))
	} else {
		spec := mapNode(specNode)

		// os (не обязателен, но если есть — name обязателен и валиден)
		if osNode, ok := spec["os"]; ok {
			osMap := mapNode(osNode)
			if osMap == nil {
				errs = append(errs, fmt.Sprintf("%s:%d os must be a mapping", fileBase, osNode.Line))
			} else if nameNode, ok := osMap["name"]; !ok {
				errs = append(errs, fmt.Sprintf("%s: os.name is required", fileBase))
			} else if nameNode.Value != "linux" && nameNode.Value != "windows" {
				errs = append(errs, fmt.Sprintf("%s:%d os has unsupported value '%s'", fileBase, nameNode.Line, nameNode.Value))
			}
		}

		// containers — обязателен
		containersNode, hasContainers := spec["containers"]
		if !hasContainers || containersNode.Kind != yaml.SequenceNode || len(containersNode.Content) == 0 {
			errs = append(errs, fmt.Sprintf("%s: spec.containers is required", fileBase))
		} else {
			for _, containerNode := range containersNode.Content {
				if containerNode.Kind != yaml.MappingNode {
					errs = append(errs, fmt.Sprintf("%s: container must be a mapping", fileBase))
					continue
				}
				containerErrs := validateContainer(filePath, containerNode)
				errs = append(errs, containerErrs...)
			}
		}
	}

	return errs
}

func validateContainer(filePath string, node *yaml.Node) []string {
	fileBase := filepath.Base(filePath)
	var errs []string
	fields := mapNode(node)

	// name — обязателен, не пустой, snake_case
	if nameNode, ok := fields["name"]; !ok || nameNode.Value == "" {
		if !ok {
			errs = append(errs, fmt.Sprintf("%s: name is required", fileBase))
		} else {
			errs = append(errs, fmt.Sprintf("%s:%d name is required", fileBase, nameNode.Line))
		}
	} else if !isValidSnakeCase(nameNode.Value) {
		errs = append(errs, fmt.Sprintf("%s:%d name has invalid format '%s'", fileBase, nameNode.Line, nameNode.Value))
	}

	// image — обязателен, не пустой, правильный формат
	if imgNode, ok := fields["image"]; !ok || imgNode.Value == "" {
		if !ok {
			errs = append(errs, fmt.Sprintf("%s: image is required", fileBase))
		} else {
			errs = append(errs, fmt.Sprintf("%s:%d image is required", fileBase, imgNode.Line))
		}
	} else if !isValidImage(imgNode.Value) {
		errs = append(errs, fmt.Sprintf("%s:%d image has invalid format '%s'", fileBase, imgNode.Line, imgNode.Value))
	}

	// ports — не обязателен
	if portsNode, ok := fields["ports"]; ok {
		if portsNode.Kind != yaml.SequenceNode {
			errs = append(errs, fmt.Sprintf("%s:%d ports must be a sequence", fileBase, portsNode.Line))
		} else {
			for _, portNode := range portsNode.Content {
				if portNode.Kind != yaml.MappingNode {
					errs = append(errs, fmt.Sprintf("%s: port must be a mapping", fileBase))
					continue
				}
				portFields := mapNode(portNode)
				if cpNode, ok := portFields["containerPort"]; !ok {
					errs = append(errs, fmt.Sprintf("%s: containerPort is required", fileBase))
				} else if port, ok := getIntValue(cpNode); !ok {
					errs = append(errs, fmt.Sprintf("%s:%d containerPort must be int", fileBase, cpNode.Line))
				} else if port <= 0 || port >= 65536 {
					errs = append(errs, fmt.Sprintf("%s:%d containerPort value out of range", fileBase, cpNode.Line))
				}

				if protoNode, ok := portFields["protocol"]; ok {
					if protoNode.Value != "TCP" && protoNode.Value != "UDP" {
						errs = append(errs, fmt.Sprintf("%s:%d protocol has unsupported value '%s'", fileBase, protoNode.Line, protoNode.Value))
					}
				}
			}
		}
	}

	// readinessProbe и livenessProbe
	for _, probeName := range []string{"readinessProbe", "livenessProbe"} {
		if probeNode, ok := fields[probeName]; ok {
			if probeNode.Kind != yaml.MappingNode {
				errs = append(errs, fmt.Sprintf("%s:%d %s must be a mapping", fileBase, probeNode.Line, probeName))
				continue
			}
			probeMap := mapNode(probeNode)
			httpGetNode, hasHTTP := probeMap["httpGet"]
			if !hasHTTP {
				errs = append(errs, fmt.Sprintf("%s: httpGet is required", fileBase))
			} else {
				if httpGetNode.Kind != yaml.MappingNode {
					errs = append(errs, fmt.Sprintf("%s:%d httpGet must be a mapping", fileBase, httpGetNode.Line))
				} else {
					httpFields := mapNode(httpGetNode)
					// path
					if pathNode, ok := httpFields["path"]; !ok {
						errs = append(errs, fmt.Sprintf("%s: path is required", fileBase))
					} else if !strings.HasPrefix(pathNode.Value, "/") {
						errs = append(errs, fmt.Sprintf("%s:%d path has invalid format '%s'", fileBase, pathNode.Line, pathNode.Value))
					}
					// port
					if portNode, ok := httpFields["port"]; !ok {
						errs = append(errs, fmt.Sprintf("%s: port is required", fileBase))
					} else if port, ok := getIntValue(portNode); !ok {
						errs = append(errs, fmt.Sprintf("%s:%d port must be int", fileBase, portNode.Line))
					} else if port <= 0 || port >= 65536 {
						errs = append(errs, fmt.Sprintf("%s:%d port value out of range", fileBase, portNode.Line))
					}
				}
			}
		}
	}

	// resources — обязателен
	if resNode, ok := fields["resources"]; !ok {
		errs = append(errs, fmt.Sprintf("%s: resources is required", fileBase))
	} else {
		if resNode.Kind != yaml.MappingNode {
			errs = append(errs, fmt.Sprintf("%s:%d resources must be a mapping", fileBase, resNode.Line))
		} else {
			resFields := mapNode(resNode)
			for _, section := range []string{"limits", "requests"} {
				if secNode, ok := resFields[section]; ok {
					if secNode.Kind != yaml.MappingNode {
						errs = append(errs, fmt.Sprintf("%s:%d resources.%s must be a mapping", fileBase, secNode.Line, section))
					} else {
						secFields := mapNode(secNode)
						// cpu
						if cpuNode, ok := secFields["cpu"]; ok {
							if _, ok := getIntValue(cpuNode); !ok {
								errs = append(errs, fmt.Sprintf("%s:%d cpu must be integer", fileBase, cpuNode.Line))
							}
						}
						// memory
						if memNode, ok := secFields["memory"]; ok {
							if memNode.Kind != yaml.ScalarNode || memNode.Tag != "!!str" {
								errs = append(errs, fmt.Sprintf("%s:%d memory must be string", fileBase, memNode.Line))
							} else if !isValidMemory(memNode.Value) {
								errs = append(errs, fmt.Sprintf("%s:%d memory has invalid format '%s'", fileBase, memNode.Line, memNode.Value))
							}
						}
					}
				}
			}
		}
	}

	return errs
}