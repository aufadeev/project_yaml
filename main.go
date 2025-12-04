package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: yamlvalid <file>")
		os.Exit(1)
	}
	file := os.Args[1]

	data, err := os.ReadFile(file)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	doc := root.Content[0]
	if errs := validateTop(file, doc); len(errs) > 0 {
		for _, e := range errs {
			fmt.Println(e)
		}
		os.Exit(1)
	}

	os.Exit(0)
}

// helper to convert mapping node to map[string]*yaml.Node
func mapNode(n *yaml.Node) map[string]*yaml.Node {
	m := map[string]*yaml.Node{}
	for i := 0; i < len(n.Content); i += 2 {
		key := n.Content[i].Value
		val := n.Content[i+1]
		m[key] = val
	}
	return m
}

func validateTop(file string, doc *yaml.Node) []string {
	var errs []string
	fileBase := filepath.Base(file)

	fields := mapNode(doc)

	// apiVersion
	if api, ok := fields["apiVersion"]; !ok {
		errs = append(errs, fmt.Sprintf("%s: apiVersion is required", fileBase))
	} else if api.Value != "v1" {
		errs = append(errs, fmt.Sprintf("%s:%d apiVersion has unsupported value '%s'", fileBase, api.Line, api.Value))
	}

	// kind
	if kind, ok := fields["kind"]; !ok {
		errs = append(errs, fmt.Sprintf("%s: kind is required", fileBase))
	} else if kind.Value != "Pod" {
		errs = append(errs, fmt.Sprintf("%s:%d kind has unsupported value '%s'", fileBase, kind.Line, kind.Value))
	}

	// metadata
	meta, ok := fields["metadata"]
	if !ok {
		errs = append(errs, fmt.Sprintf("%s: metadata is required", fileBase))
	} else {
		metaMap := mapNode(meta)
		if _, ok := metaMap["name"]; !ok {
			errs = append(errs, fmt.Sprintf("%s: metadata.name is required", fileBase))
		}
	}

	// spec
	spec, ok := fields["spec"]
	if !ok {
		errs = append(errs, fmt.Sprintf("%s: spec is required", fileBase))
	} else {
		specMap := mapNode(spec)

		// os
		if osNode, ok := specMap["os"]; ok {
			if osNode.Value != "linux" && osNode.Value != "windows" {
				errs = append(errs, fmt.Sprintf("%s:%d os has unsupported value '%s'", fileBase, osNode.Line, osNode.Value))
			}
		}

		// containers
		contNode, ok := specMap["containers"]
		if !ok || contNode.Kind != yaml.SequenceNode || len(contNode.Content) == 0 {
			errs = append(errs, fmt.Sprintf("%s: spec.containers is required", fileBase))
		} else {
			for i, c := range contNode.Content {
				cErrs := validateContainer(fileBase, i, c)
				errs = append(errs, cErrs...)
			}
		}
	}

	return errs
}

func validateContainer(fileBase string, idx int, node *yaml.Node) []string {
	var errs []string
	cmap := mapNode(node)

	// name
	if name, ok := cmap["name"]; !ok || strings.Contains(name.Value, " ") {
		errs = append(errs, fmt.Sprintf("%s: containers[%d].name has invalid format", fileBase, idx))
	}

	// image
	if img, ok := cmap["image"]; !ok || !strings.HasPrefix(img.Value, "registry.bigbrother.io/") || !strings.Contains(img.Value, ":") {
		errs = append(errs, fmt.Sprintf("%s: containers[%d].image has invalid format", fileBase, idx))
	}

	// ports
	if portsNode, ok := cmap["ports"]; ok && portsNode.Kind == yaml.SequenceNode {
		for j, p := range portsNode.Content {
			pm := mapNode(p)
			cp := pm["containerPort"].Value
			portVal := 0
			fmt.Sscanf(cp, "%d", &portVal)
			if portVal <= 0 || portVal >= 65536 {
				errs = append(errs, fmt.Sprintf("%s: containers[%d].ports[%d].containerPort out of range", fileBase, idx, j))
			}
			if proto, ok := pm["protocol"]; ok {
				if proto.Value != "TCP" && proto.Value != "UDP" {
					errs = append(errs, fmt.Sprintf("%s: containers[%d].ports[%d].protocol has unsupported value '%s'", fileBase, idx, j, proto.Value))
				}
			}
		}
	}

	// probes
	for _, pname := range []string{"readinessProbe", "livenessProbe"} {
		if probeNode, ok := cmap[pname]; ok {
			probeMap := mapNode(probeNode)
			httpGet, ok := probeMap["httpGet"]
			if !ok {
				errs = append(errs, fmt.Sprintf("%s: containers[%d].%s.httpGet is required", fileBase, idx, pname))
				continue
			}
			httpMap := mapNode(httpGet)
			path := httpMap["path"]
			port := httpMap["port"]
			if !strings.HasPrefix(path.Value, "/") {
				errs = append(errs, fmt.Sprintf("%s: containers[%d].%s.path invalid", fileBase, idx, pname))
			}
			portVal := 0
			fmt.Sscanf(port.Value, "%d", &portVal)
			if portVal <= 0 || portVal >= 65536 {
				errs = append(errs, fmt.Sprintf("%s: containers[%d].%s.port out of range", fileBase, idx, pname))
			}
		}
	}

	// resources
	resNode, ok := cmap["resources"]
	if !ok {
		errs = append(errs, fmt.Sprintf("%s: containers[%d].resources is required", fileBase, idx))
	} else {
		resMap := mapNode(resNode)
		for _, f := range []string{"limits", "requests"} {
			if rNode, ok := resMap[f]; ok {
				rMap := mapNode(rNode)

				// memory
				if memNode, ok := rMap["memory"]; ok {
					if !strings.HasSuffix(memNode.Value, "Mi") && !strings.HasSuffix(memNode.Value, "Gi") && !strings.HasSuffix(memNode.Value, "Ki") {
						errs = append(errs, fmt.Sprintf("%s: containers[%d].resources.%s.memory invalid", fileBase, idx, f))
					}
				}

				// cpu
				if cpuNode, ok := rMap["cpu"]; ok {
					var cpuVal int
					if _, err := fmt.Sscanf(cpuNode.Value, "%d", &cpuVal); err != nil {
						errs = append(errs, fmt.Sprintf("%s:%d cpu must be int", fileBase, cpuNode.Line))
					}
				}
			}
		}
	}

	return errs
}