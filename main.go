package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

var (
	validOSNames      = map[string]bool{"linux": true, "windows": true}
	validProtocols    = map[string]bool{"TCP": true, "UDP": true}
	validResourceKeys = map[string]bool{"cpu": true, "memory": true}
	memoryUnitPattern = regexp.MustCompile(`^\d+(Gi|Mi|Ki)$`)
	snakeCasePattern  = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <yaml-file>\n", os.Args[0])
		os.Exit(1)
	}

	filename := os.Args[1]
	content, err := os.ReadFile(filename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: cannot read file: %v\n", filename, err)
		os.Exit(1)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		// yaml.v3 не дает номер строки ошибки напрямую, но можно попробовать
		fmt.Fprintf(os.Stderr, "%s: invalid YAML: %v\n", filename, err)
		os.Exit(1)
	}

	if len(root.Content) == 0 {
		fmt.Fprintf(os.Stderr, "%s: empty YAML document\n", filename)
		os.Exit(1)
	}

	// Предполагаем один документ
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		fmt.Fprintf(os.Stderr, "%s:1 root must be a mapping\n", filename)
		os.Exit(1)
	}

	// Извлекаем поля верхнего уровня
	apiVersion := findNode(doc, "apiVersion")
	kind := findNode(doc, "kind")
	metadata := findNode(doc, "metadata")
	spec := findNode(doc, "spec")

	var allErrors []string

	// Проверка обязательных полей верхнего уровня
	if apiVersion == nil {
		allErrors = append(allErrors, "apiVersion is required")
	} else if apiVersion.Value != "v1" {
		allErrors = append(allErrors, fmt.Sprintf("apiVersion has unsupported value '%s'", apiVersion.Value))
	}

	if kind == nil {
		allErrors = append(allErrors, "kind is required")
	} else if kind.Value != "Pod" {
		allErrors = append(allErrors, fmt.Sprintf("kind has unsupported value '%s'", kind.Value))
	}

	if metadata == nil {
		allErrors = append(allErrors, "metadata is required")
	} else if err := validateObjectMeta(filename, metadata, &allErrors); err != nil {
		// ошибки уже добавлены внутрь allErrors
	}

	if spec == nil {
		allErrors = append(allErrors, "spec is required")
	} else if err := validatePodSpec(filename, spec, &allErrors); err != nil {
		// ошибки уже добавлены
	}

	if len(allErrors) > 0 {
		for _, msg := range allErrors {
			fmt.Fprintf(os.Stderr, "%s\n", msg)
		}
		os.Exit(1)
	}

	os.Exit(0)
}

// findNode ищет ключ в маппинге (yaml.MappingNode)
func findNode(parent *yaml.Node, key string) *yaml.Node {
	if parent.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(parent.Content); i += 2 {
		k := parent.Content[i]
		if k.Value == key {
			return parent.Content[i+1]
		}
	}
	return nil
}

// validateObjectMeta проверяет metadata
func validateObjectMeta(filename string, meta *yaml.Node, errs *[]string) error {
	if meta.Kind != yaml.MappingNode {
		*errs = append(*errs, fmt.Sprintf("%s:%d metadata must be an object", filename, meta.Line))
		return nil
	}

	name := findNode(meta, "name")
	if name == nil {
		*errs = append(*errs, "metadata.name is required")
	} else if name.Kind != yaml.ScalarNode {
		*errs = append(*errs, fmt.Sprintf("%s:%d metadata.name must be string", filename, name.Line))
	}

	// namespace не обязателен
	// labels не обязателен, но если есть — должен быть маппингом
	labels := findNode(meta, "labels")
	if labels != nil && labels.Kind != yaml.MappingNode {
		*errs = append(*errs, fmt.Sprintf("%s:%d metadata.labels must be an object", filename, labels.Line))
	}

	return nil
}

// validatePodSpec проверяет spec
func validatePodSpec(filename string, spec *yaml.Node, errs *[]string) error {
	if spec.Kind != yaml.MappingNode {
		*errs = append(*errs, fmt.Sprintf("%s:%d spec must be an object", filename, spec.Line))
		return nil
	}

	// os (необязательно)
	osNode := findNode(spec, "os")
	if osNode != nil {
		if osNode.Kind != yaml.MappingNode {
			*errs = append(*errs, fmt.Sprintf("%s:%d spec.os must be an object", filename, osNode.Line))
		} else {
			osNameNode := findNode(osNode, "name")
			if osNameNode == nil {
				*errs = append(*errs, "spec.os.name is required")
			} else if !validOSNames[osNameNode.Value] {
				*errs = append(*errs, fmt.Sprintf("%s:%d spec.os.name has unsupported value '%s'", filename, osNameNode.Line, osNameNode.Value))
			}
		}
	}

	// containers (обязательно)
	containers := findNode(spec, "containers")
	if containers == nil {
		*errs = append(*errs, "spec.containers is required")
		return nil
	}
	if containers.Kind != yaml.SequenceNode {
		*errs = append(*errs, fmt.Sprintf("%s:%d spec.containers must be a list", filename, containers.Line))
		return nil
	}

	if len(containers.Content) == 0 {
		*errs = append(*errs, fmt.Sprintf("%s:%d spec.containers must not be empty", filename, containers.Line))
	}

	seenNames := make(map[string]bool)
	for _, container := range containers.Content {
		if container.Kind != yaml.MappingNode {
			*errs = append(*errs, fmt.Sprintf("%s:%d container must be an object", filename, container.Line))
			continue
		}
		validateContainer(filename, container, seenNames, errs)
	}

	return nil
}

// validateContainer проверяет один контейнер
func validateContainer(filename string, cont *yaml.Node, seenNames map[string]bool, errs *[]string) {
	nameNode := findNode(cont, "name")
	imageNode := findNode(cont, "image")
	portsNode := findNode(cont, "ports")
	readinessProbeNode := findNode(cont, "readinessProbe")
	livenessProbeNode := findNode(cont, "livenessProbe")
	resourcesNode := findNode(cont, "resources")

	// name (обязательно)
	if nameNode == nil {
		*errs = append(*errs, "containers[].name is required")
	} else if nameNode.Kind != yaml.ScalarNode {
		*errs = append(*errs, fmt.Sprintf("%s:%d containers[].name must be string", filename, nameNode.Line))
	} else {
		if !snakeCasePattern.MatchString(nameNode.Value) {
			*errs = append(*errs, fmt.Sprintf("%s:%d containers[].name has invalid format '%s'", filename, nameNode.Line, nameNode.Value))
		} else if seenNames[nameNode.Value] {
			*errs = append(*errs, fmt.Sprintf("%s:%d containers[].name '%s' is not unique", filename, nameNode.Line, nameNode.Value))
		} else {
			seenNames[nameNode.Value] = true
		}
	}

	// image (обязательно)
	if imageNode == nil {
		*errs = append(*errs, "containers[].image is required")
	} else if imageNode.Kind != yaml.ScalarNode {
		*errs = append(*errs, fmt.Sprintf("%s:%d containers[].image must be string", filename, imageNode.Line))
	} else {
		img := imageNode.Value
		if !strings.HasPrefix(img, "registry.bigbrother.io/") {
			*errs = append(*errs, fmt.Sprintf("%s:%d containers[].image has invalid format '%s'", filename, imageNode.Line, img))
		} else {
			parts := strings.Split(img, ":")
			if len(parts) < 2 || parts[len(parts)-1] == "" {
				*errs = append(*errs, fmt.Sprintf("%s:%d containers[].image has invalid format '%s' (missing tag)", filename, imageNode.Line, img))
			}
		}
	}

	// ports (опционально)
	if portsNode != nil {
		if portsNode.Kind != yaml.SequenceNode {
			*errs = append(*errs, fmt.Sprintf("%s:%d containers[].ports must be a list", filename, portsNode.Line))
		} else {
			for _, portNode := range portsNode.Content {
				if portNode.Kind != yaml.MappingNode {
					*errs = append(*errs, fmt.Sprintf("%s:%d containers[].ports[] must be an object", filename, portNode.Line))
					continue
				}
				validateContainerPort(filename, portNode, errs)
			}
		}
	}

	// readinessProbe (опционально)
	if readinessProbeNode != nil {
		if readinessProbeNode.Kind != yaml.MappingNode {
			*errs = append(*errs, fmt.Sprintf("%s:%d containers[].readinessProbe must be an object", filename, readinessProbeNode.Line))
		} else {
			validateProbe(filename, readinessProbeNode, "readinessProbe", errs)
		}
	}

	// livenessProbe (опционально)
	if livenessProbeNode != nil {
		if livenessProbeNode.Kind != yaml.MappingNode {
			*errs = append(*errs, fmt.Sprintf("%s:%d containers[].livenessProbe must be an object", filename, livenessProbeNode.Line))
		} else {
			validateProbe(filename, livenessProbeNode, "livenessProbe", errs)
		}
	}

	// resources (обязательно)
	if resourcesNode == nil {
		*errs = append(*errs, "containers[].resources is required")
	} else if resourcesNode.Kind != yaml.MappingNode {
		*errs = append(*errs, fmt.Sprintf("%s:%d containers[].resources must be an object", filename, resourcesNode.Line))
	} else {
		validateResourceRequirements(filename, resourcesNode, errs)
	}
}

// validateContainerPort
func validateContainerPort(filename string, portNode *yaml.Node, errs *[]string) {
	containerPortNode := findNode(portNode, "containerPort")
	protocolNode := findNode(portNode, "protocol")

	if containerPortNode == nil {
		*errs = append(*errs, "containers[].ports[].containerPort is required")
	} else {
		if containerPortNode.Kind != yaml.ScalarNode {
			*errs = append(*errs, fmt.Sprintf("%s:%d containers[].ports[].containerPort must be int", filename, containerPortNode.Line))
		} else {
			val, err := strconv.Atoi(containerPortNode.Value)
			if err != nil {
				*errs = append(*errs, fmt.Sprintf("%s:%d containers[].ports[].containerPort must be int", filename, containerPortNode.Line))
			} else if val <= 0 || val >= 65536 {
				*errs = append(*errs, fmt.Sprintf("%s:%d containers[].ports[].containerPort value out of range", filename, containerPortNode.Line))
			}
		}
	}

	if protocolNode != nil {
		if protocolNode.Kind != yaml.ScalarNode {
			*errs = append(*errs, fmt.Sprintf("%s:%d containers[].ports[].protocol must be string", filename, protocolNode.Line))
		} else if !validProtocols[protocolNode.Value] {
			*errs = append(*errs, fmt.Sprintf("%s:%d containers[].ports[].protocol has unsupported value '%s'", filename, protocolNode.Line, protocolNode.Value))
		}
	}
}

// validateProbe
func validateProbe(filename string, probeNode *yaml.Node, probeName string, errs *[]string) {
	httpGetNode := findNode(probeNode, "httpGet")
	if httpGetNode == nil {
		*errs = append(*errs, fmt.Sprintf("containers[].%s.httpGet is required", probeName))
	} else if httpGetNode.Kind != yaml.MappingNode {
		*errs = append(*errs, fmt.Sprintf("%s:%d containers[].%s.httpGet must be an object", filename, httpGetNode.Line, probeName))
	} else {
		pathNode := findNode(httpGetNode, "path")
		portNode := findNode(httpGetNode, "port")

		if pathNode == nil {
			*errs = append(*errs, fmt.Sprintf("containers[].%s.httpGet.path is required", probeName))
		} else if pathNode.Kind != yaml.ScalarNode {
			*errs = append(*errs, fmt.Sprintf("%s:%d containers[].%s.httpGet.path must be string", filename, pathNode.Line, probeName))
		} else if !strings.HasPrefix(pathNode.Value, "/") {
			*errs = append(*errs, fmt.Sprintf("%s:%d containers[].%s.httpGet.path has invalid format '%s'", filename, pathNode.Line, probeName, pathNode.Value))
		}

		if portNode == nil {
			*errs = append(*errs, fmt.Sprintf("containers[].%s.httpGet.port is required", probeName))
		} else if portNode.Kind != yaml.ScalarNode {
			*errs = append(*errs, fmt.Sprintf("%s:%d containers[].%s.httpGet.port must be int", filename, portNode.Line, probeName))
		} else {
			val, err := strconv.Atoi(portNode.Value)
			if err != nil {
				*errs = append(*errs, fmt.Sprintf("%s:%d containers[].%s.httpGet.port must be int", filename, portNode.Line, probeName))
			} else if val <= 0 || val >= 65536 {
				*errs = append(*errs, fmt.Sprintf("%s:%d containers[].%s.httpGet.port value out of range", filename, portNode.Line, probeName))
			}
		}
	}
}

// validateResourceRequirements
func validateResourceRequirements(filename string, rrNode *yaml.Node, errs *[]string) {
	requestsNode := findNode(rrNode, "requests")
	limitsNode := findNode(rrNode, "limits")

	if requestsNode != nil {
		if requestsNode.Kind != yaml.MappingNode {
			*errs = append(*errs, fmt.Sprintf("%s:%d containers[].resources.requests must be an object", filename, requestsNode.Line))
		} else {
			validateResourceObject(filename, requestsNode, "requests", errs)
		}
	}

	if limitsNode != nil {
		if limitsNode.Kind != yaml.MappingNode {
			*errs = append(*errs, fmt.Sprintf("%s:%d containers[].resources.limits must be an object", filename, limitsNode.Line))
		} else {
			validateResourceObject(filename, limitsNode, "limits", errs)
		}
	}
}

func validateResourceObject(filename string, objNode *yaml.Node, objName string, errs *[]string) {
	for i := 0; i < len(objNode.Content); i += 2 {
		keyNode := objNode.Content[i]
		valNode := objNode.Content[i+1]

		if keyNode.Kind != yaml.ScalarNode {
			*errs = append(*errs, fmt.Sprintf("%s:%d containers[].resources.%s keys must be strings", filename, keyNode.Line, objName))
			continue
		}

		key := keyNode.Value
		if !validResourceKeys[key] {
			// Разрешены только cpu и memory
			continue
		}

		if key == "cpu" {
			if valNode.Kind != yaml.ScalarNode {
				*errs = append(*errs, fmt.Sprintf("%s:%d containers[].resources.%s.cpu must be int", filename, valNode.Line, objName))
			} else {
				if _, err := strconv.Atoi(valNode.Value); err != nil {
					*errs = append(*errs, fmt.Sprintf("%s:%d containers[].resources.%s.cpu must be int", filename, valNode.Line, objName))
				}
			}
		} else if key == "memory" {
			if valNode.Kind != yaml.ScalarNode {
				*errs = append(*errs, fmt.Sprintf("%s:%d containers[].resources.%s.memory must be string", filename, valNode.Line, objName))
			} else if !memoryUnitPattern.MatchString(valNode.Value) {
				*errs = append(*errs, fmt.Sprintf("%s:%d containers[].resources.%s.memory has invalid format '%s'", filename, valNode.Line, objName, valNode.Value))
			}
		}
	}
}