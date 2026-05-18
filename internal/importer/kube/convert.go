package kube

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/s1liconcow/skiff/internal/spec"
)

type Options struct {
	Name string
	Env  string
}

type Result struct {
	OK             bool             `json:"ok"`
	Service        spec.Document    `json:"service"`
	SkiffYAML      string           `json:"skiff_yaml"`
	MarkdownReport string           `json:"markdown_report"`
	Imported       []ImportedObject `json:"imported"`
	Findings       []Finding        `json:"findings,omitempty"`
}

type ImportedObject struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	Action    string `json:"action"`
}

type Finding struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Path     string `json:"path,omitempty"`
	Summary  string `json:"summary"`
}

func Convert(objects []Object, opts Options) (*Result, error) {
	if len(objects) == 0 {
		return nil, fmt.Errorf("at least one Kubernetes object is required")
	}
	ctx := conversionContext{objects: objects}
	ctx.index()
	ctx.findUnsupportedObjects()

	deployment := ctx.firstWorkload("Deployment")
	if deployment == nil {
		statefulSet := ctx.firstWorkload("StatefulSet")
		if statefulSet != nil {
			ctx.findUnsupportedPodFeatures(*statefulSet)
			service := ctx.matchingService(*statefulSet)
			doc := ctx.statefulGroupProposal(*statefulSet, service, opts)
			return ctx.finish(doc)
		}
		ctx.error("KUBE_DEPLOYMENT_REQUIRED", "$", "simple imports require an apps/v1 Deployment")
		return ctx.finish(spec.Document{})
	}
	ctx.findUnsupportedPodFeatures(*deployment)

	service := ctx.matchingService(*deployment)
	ingress := ctx.matchingIngress(service)
	hpa := ctx.matchingHPA(*deployment)
	pdb := ctx.matchingPDB(*deployment)
	if pdb != nil {
		ctx.warn("KUBE_PDB_REVIEW_REQUIRED", objectPath(*pdb), "PodDisruptionBudget has no direct Skiff service field; review rollout and minimum capacity settings")
	}

	doc := ctx.serviceSpec(*deployment, service, ingress, hpa, opts)
	return ctx.finish(doc)
}

type conversionContext struct {
	objects    []Object
	configMaps map[string]map[string]string
	findings   []Finding
	imported   []ImportedObject
}

func (c *conversionContext) index() {
	c.configMaps = map[string]map[string]string{}
	for _, object := range c.objects {
		c.imported = append(c.imported, ImportedObject{
			Kind:      object.Kind,
			Name:      object.Metadata.Name,
			Namespace: object.Metadata.Namespace,
			Action:    actionForKind(object.Kind),
		})
		if object.Kind == "ConfigMap" {
			c.configMaps[namespacedName(object)] = stringMap(object.Raw["data"])
		}
	}
	sort.Slice(c.imported, func(i, j int) bool {
		if c.imported[i].Kind == c.imported[j].Kind {
			return c.imported[i].Name < c.imported[j].Name
		}
		return c.imported[i].Kind < c.imported[j].Kind
	})
}

func (c *conversionContext) findUnsupportedObjects() {
	for _, object := range c.objects {
		switch object.Kind {
		case "Deployment", "Service", "Ingress", "HorizontalPodAutoscaler", "ConfigMap", "Secret", "PodDisruptionBudget":
		case "StatefulSet":
			c.warn("KUBE_STATEFULSET_PROPOSAL", objectPath(object), "StatefulSet was converted only into a Skiff StatefulGroup proposal; review identity, volumes, and recipe behavior before applying")
		case "DaemonSet", "Job", "CronJob":
			c.warn("KUBE_WORKLOAD_REVIEW_REQUIRED", objectPath(object), object.Kind+" is not converted into the Skiff Service spec")
		default:
			if strings.Contains(strings.ToLower(object.APIVersion), "apiextensions.k8s.io") || object.Kind == "CustomResourceDefinition" {
				c.error("KUBE_CRD_UNSUPPORTED", objectPath(object), "CustomResourceDefinition cannot be represented as a Skiff service")
				continue
			}
			c.warn("KUBE_OBJECT_IGNORED", objectPath(object), object.Kind+" is not used by the simple service importer")
		}
	}
}

func (c *conversionContext) firstWorkload(kind string) *Object {
	for i := range c.objects {
		if c.objects[i].Kind == kind {
			return &c.objects[i]
		}
	}
	return nil
}

func (c *conversionContext) matchingService(deployment Object) *Object {
	templateLabels := stringMap(mapAt(mapAt(mapAt(deployment.Raw, "spec"), "template"), "metadata")["labels"])
	for i := range c.objects {
		object := &c.objects[i]
		if object.Kind != "Service" {
			continue
		}
		selector := stringMap(mapAt(object.Raw, "spec")["selector"])
		if selectorMatches(selector, templateLabels) {
			return object
		}
	}
	c.warn("KUBE_SERVICE_MISSING", objectPath(deployment), "no Service selector matched the Deployment; generated Skiff service will be private")
	return nil
}

func (c *conversionContext) matchingIngress(service *Object) *Object {
	if service == nil {
		return nil
	}
	serviceName := service.Metadata.Name
	for i := range c.objects {
		object := &c.objects[i]
		if object.Kind != "Ingress" {
			continue
		}
		if ingressReferencesService(*object, serviceName) {
			return object
		}
	}
	return nil
}

func (c *conversionContext) matchingHPA(deployment Object) *Object {
	for i := range c.objects {
		object := &c.objects[i]
		if object.Kind != "HorizontalPodAutoscaler" {
			continue
		}
		ref := mapAt(mapAt(object.Raw, "spec"), "scaleTargetRef")
		if stringAt(ref, "kind") == "Deployment" && stringAt(ref, "name") == deployment.Metadata.Name {
			return object
		}
	}
	return nil
}

func (c *conversionContext) matchingPDB(deployment Object) *Object {
	templateLabels := stringMap(mapAt(mapAt(mapAt(deployment.Raw, "spec"), "template"), "metadata")["labels"])
	for i := range c.objects {
		object := &c.objects[i]
		if object.Kind != "PodDisruptionBudget" {
			continue
		}
		selector := stringMap(mapAt(mapAt(mapAt(object.Raw, "spec"), "selector"), "matchLabels"))
		if selectorMatches(selector, templateLabels) {
			return object
		}
	}
	return nil
}

func (c *conversionContext) findUnsupportedPodFeatures(deployment Object) {
	templateSpec := mapAt(mapAt(mapAt(deployment.Raw, "spec"), "template"), "spec")
	containers := mapList(templateSpec["containers"])
	if len(containers) > 1 {
		names := make([]string, 0, len(containers)-1)
		for _, container := range containers[1:] {
			names = append(names, stringAt(container, "name"))
		}
		c.warn("KUBE_SIDECAR_IGNORED", objectPath(deployment)+".spec.template.spec.containers", "only the first container is imported; sidecars need explicit plugins or addon design: "+strings.Join(nonEmptyStrings(names), ", "))
	}
	if len(mapList(templateSpec["initContainers"])) > 0 {
		c.warn("KUBE_INIT_CONTAINERS_IGNORED", objectPath(deployment)+".spec.template.spec.initContainers", "init containers are not imported; convert startup behavior into the artifact or a plugin")
	}
	for i, volume := range mapList(templateSpec["volumes"]) {
		if _, ok := volume["hostPath"]; ok {
			c.error("KUBE_HOSTPATH_UNSUPPORTED", fmt.Sprintf("%s.spec.template.spec.volumes[%d]", objectPath(deployment), i), "hostPath volumes cannot be represented in a default Skiff VM workload")
		}
	}
	for i, container := range containers {
		securityContext := mapAt(container, "securityContext")
		if boolAt(securityContext, "privileged") {
			c.error("KUBE_PRIVILEGED_UNSUPPORTED", fmt.Sprintf("%s.spec.template.spec.containers[%d].securityContext.privileged", objectPath(deployment), i), "privileged containers require an explicit security review and are not imported")
		}
	}
	for key, value := range deployment.Metadata.Annotations {
		if isServiceMeshAnnotation(key) {
			c.warn("KUBE_SERVICE_MESH_ANNOTATION", objectPath(deployment)+".metadata.annotations."+key, "service mesh annotation is not imported: "+value)
		}
	}
	templateMeta := mapAt(mapAt(deployment.Raw, "spec"), "template")
	for key, value := range stringMap(mapAt(templateMeta, "metadata")["annotations"]) {
		if isServiceMeshAnnotation(key) {
			c.warn("KUBE_SERVICE_MESH_ANNOTATION", objectPath(deployment)+".spec.template.metadata.annotations."+key, "service mesh annotation is not imported: "+value)
		}
	}
}

func (c *conversionContext) serviceSpec(deployment Object, service, ingress, hpa *Object, opts Options) spec.Document {
	specMap := mapAt(deployment.Raw, "spec")
	templateSpec := mapAt(mapAt(mapAt(deployment.Raw, "spec"), "template"), "spec")
	containers := mapList(templateSpec["containers"])
	container := map[string]any{}
	if len(containers) > 0 {
		container = containers[0]
	}
	name := firstNonEmpty(opts.Name, deployment.Metadata.Name)
	env := firstNonEmpty(opts.Env, deployment.Metadata.Namespace, "staging")
	image := stringAt(container, "image")
	if image == "" {
		c.error("KUBE_IMAGE_REQUIRED", objectPath(deployment)+".spec.template.spec.containers[0].image", "container image is required")
		image = "missing-image"
	}
	if isProductionEnv(env) && !strings.Contains(image, "@sha256:") {
		c.warn("KUBE_IMAGE_DIGEST_RECOMMENDED", objectPath(deployment)+".spec.template.spec.containers[0].image", "production Skiff specs should use digest-pinned OCI images")
	}
	port, portName := c.runtimePort(container, service)
	health := c.health(container, port, portName)
	min, max := scale(specMap, hpa)
	runtimeEnv, secrets := c.envAndSecrets(deployment, container)
	doc := spec.Document{
		APIVersion: spec.APIVersion,
		Kind:       spec.KindService,
		Metadata: spec.Metadata{
			Name: name,
			Env:  env,
			Labels: map[string]string{
				"imported-from": "kubernetes",
			},
		},
		Artifact: &spec.Artifact{Type: "oci", Ref: image},
		Runtime: spec.Runtime{
			Port: port,
			Env:  runtimeEnv,
			Health: spec.Health{
				Type: "http",
				Path: health,
				Port: port,
			},
		},
		Scale:   spec.Scale{Min: min, Max: max},
		Secrets: secrets,
	}
	if command := stringSlice(container["command"]); len(command) > 0 {
		doc.Runtime.Command = command
	}
	if ingress != nil {
		c.applyIngress(&doc, *ingress)
	} else if service != nil && stringAt(mapAt(service.Raw, "spec"), "type") == "LoadBalancer" {
		c.warn("KUBE_LOADBALANCER_REVIEW_REQUIRED", objectPath(*service)+".spec.type", "LoadBalancer Service was imported without public ingress; add a Skiff network.ingress host and TLS cert before production cutover")
	}
	spec.ApplyDefaults(&doc)
	return doc
}

func (c *conversionContext) statefulGroupProposal(statefulSet Object, service *Object, opts Options) spec.Document {
	specMap := mapAt(statefulSet.Raw, "spec")
	templateSpec := mapAt(mapAt(specMap, "template"), "spec")
	containers := mapList(templateSpec["containers"])
	container := map[string]any{}
	if len(containers) > 0 {
		container = containers[0]
	}
	name := firstNonEmpty(opts.Name, statefulSet.Metadata.Name)
	env := firstNonEmpty(opts.Env, statefulSet.Metadata.Namespace, "staging")
	image := stringAt(container, "image")
	if image == "" {
		c.error("KUBE_IMAGE_REQUIRED", objectPath(statefulSet)+".spec.template.spec.containers[0].image", "container image is required")
		image = "missing-image"
	}
	if isProductionEnv(env) && !strings.Contains(image, "@sha256:") {
		c.warn("KUBE_IMAGE_DIGEST_RECOMMENDED", objectPath(statefulSet)+".spec.template.spec.containers[0].image", "production Skiff specs should use digest-pinned OCI images")
	}
	port, portName := c.runtimePort(container, service)
	health := c.health(container, port, portName)
	runtimeEnv, secrets := c.envAndSecrets(statefulSet, container)
	volume := c.statefulVolume(statefulSet, container)
	recipeConfig := map[string]any{
		"artifact": map[string]any{
			"type": "oci",
			"ref":  image,
		},
		"runtime": map[string]any{
			"health": map[string]any{
				"path": health,
				"port": port,
			},
			"ports": map[string]any{
				firstNonEmpty(portName, "client"): port,
			},
		},
	}
	if command := stringSlice(container["command"]); len(command) > 0 {
		recipeConfig["runtime"].(map[string]any)["command"] = command
	}
	if len(runtimeEnv) > 0 {
		recipeConfig["runtime"].(map[string]any)["env"] = runtimeEnv
	}
	configBody, err := json.Marshal(recipeConfig)
	if err != nil {
		c.error("KUBE_STATEFULSET_CONFIG_INVALID", objectPath(statefulSet), "could not encode StatefulGroup recipe proposal: "+err.Error())
	}
	replicas := intAt(specMap, "replicas")
	if replicas == 0 {
		replicas = 1
	}
	doc := spec.Document{
		APIVersion: spec.APIVersion,
		Kind:       spec.KindStatefulGroup,
		Metadata: spec.Metadata{
			Name: name,
			Env:  env,
			Labels: map[string]string{
				"imported-from": "kubernetes",
			},
		},
		Secrets: secrets,
		StatefulGroup: &spec.StatefulGroup{
			Replicas: replicas,
			Volume:   volume,
			Identity: spec.StatefulIdentity{HostnamePrefix: name},
			Recipe: spec.StatefulRecipe{
				Name:   "kubernetes-statefulset",
				Config: configBody,
			},
			Update: spec.StatefulUpdate{Strategy: "ordered"},
		},
	}
	spec.ApplyDefaults(&doc)
	return doc
}

func (c *conversionContext) statefulVolume(statefulSet Object, container map[string]any) spec.Volume {
	volume := spec.Volume{Type: "gp3", MountPath: "/var/lib/skiff/state", Encrypted: true}
	for _, mount := range mapList(container["volumeMounts"]) {
		if path := stringAt(mount, "mountPath"); path != "" {
			volume.MountPath = path
			break
		}
	}
	claims := mapList(mapAt(statefulSet.Raw, "spec")["volumeClaimTemplates"])
	if len(claims) == 0 {
		c.warn("KUBE_STATEFULSET_VOLUME_REVIEW_REQUIRED", objectPath(statefulSet)+".spec.volumeClaimTemplates", "StatefulSet has no volumeClaimTemplates; defaulted StatefulGroup volume size to 10Gi")
		volume.Size = "10Gi"
		return volume
	}
	requests := mapAt(mapAt(mapAt(claims[0], "spec"), "resources"), "requests")
	volume.Size = firstNonEmpty(stringAt(requests, "storage"), "10Gi")
	if storageClass := stringAt(mapAt(claims[0], "spec"), "storageClassName"); storageClass != "" && !isSkiffVolumeType(storageClass) {
		c.warn("KUBE_STORAGECLASS_REVIEW_REQUIRED", objectPath(statefulSet)+".spec.volumeClaimTemplates[0].spec.storageClassName", "storageClassName "+storageClass+" does not map directly to a Skiff volume type; defaulted volume.type to gp3")
	} else if storageClass != "" {
		volume.Type = storageClass
	}
	c.warn("KUBE_STATEFULSET_VOLUME_REVIEW_REQUIRED", objectPath(statefulSet)+".spec.volumeClaimTemplates[0]", "persistent volume claim was converted to a StatefulGroup volume proposal; verify size, type, encryption, and retention")
	return volume
}

func (c *conversionContext) runtimePort(container map[string]any, service *Object) (int, string) {
	ports := mapList(container["ports"])
	if service != nil {
		for _, servicePort := range mapList(mapAt(service.Raw, "spec")["ports"]) {
			target := servicePort["targetPort"]
			if port := intValue(target); port > 0 {
				return port, stringAt(servicePort, "name")
			}
			if targetName, ok := target.(string); ok && targetName != "" {
				for _, containerPort := range ports {
					if stringAt(containerPort, "name") == targetName {
						if port := intAt(containerPort, "containerPort"); port > 0 {
							return port, targetName
						}
					}
				}
			}
			if port := intAt(servicePort, "port"); port > 0 {
				return port, stringAt(servicePort, "name")
			}
		}
	}
	for _, containerPort := range ports {
		if port := intAt(containerPort, "containerPort"); port > 0 {
			return port, stringAt(containerPort, "name")
		}
	}
	c.warn("KUBE_PORT_DEFAULTED", "$.spec.template.spec.containers[0].ports", "no container or service port was found; defaulted runtime.port to 8080")
	return 8080, ""
}

func (c *conversionContext) health(container map[string]any, port int, portName string) string {
	for _, probeName := range []string{"readinessProbe", "livenessProbe"} {
		probe := mapAt(container, probeName)
		httpGet := mapAt(probe, "httpGet")
		if path := stringAt(httpGet, "path"); path != "" {
			if probePort := intValue(httpGet["port"]); probePort > 0 && probePort != port {
				c.warn("KUBE_HEALTH_PORT_REVIEW_REQUIRED", "$.spec.template.spec.containers[0]."+probeName+".httpGet.port", "probe port differs from runtime.port; Skiff imported the path but kept runtime.port")
			}
			if probePortName, _ := httpGet["port"].(string); probePortName != "" && portName != "" && probePortName != portName {
				c.warn("KUBE_HEALTH_PORT_REVIEW_REQUIRED", "$.spec.template.spec.containers[0]."+probeName+".httpGet.port", "probe named port differs from runtime port; Skiff imported the path but kept runtime.port")
			}
			return path
		}
	}
	c.warn("KUBE_HEALTH_DEFAULTED", "$.spec.template.spec.containers[0].readinessProbe", "no HTTP readiness or liveness probe found; defaulted health check path to /")
	return "/"
}

func scale(specMap map[string]any, hpa *Object) (int, int) {
	replicas := intAt(specMap, "replicas")
	if replicas == 0 {
		replicas = 1
	}
	min := replicas
	max := replicas
	if hpa != nil {
		hpaSpec := mapAt(hpa.Raw, "spec")
		if value := intAt(hpaSpec, "minReplicas"); value > 0 {
			min = value
		}
		if value := intAt(hpaSpec, "maxReplicas"); value > 0 {
			max = value
		}
	}
	if max < min {
		max = min
	}
	return min, max
}

func (c *conversionContext) envAndSecrets(deployment Object, container map[string]any) (map[string]string, []spec.SecretRef) {
	runtimeEnv := map[string]string{}
	var secrets []spec.SecretRef
	for i, env := range mapList(container["env"]) {
		name := stringAt(env, "name")
		if name == "" {
			continue
		}
		if value := stringAt(env, "value"); value != "" {
			runtimeEnv[name] = value
			continue
		}
		valueFrom := mapAt(env, "valueFrom")
		if secretRef := mapAt(valueFrom, "secretKeyRef"); len(secretRef) > 0 {
			secretName := stringAt(secretRef, "name")
			key := stringAt(secretRef, "key")
			secrets = appendSecretRef(secrets, spec.SecretRef{
				Name: dnsName(name),
				Ref:  fmt.Sprintf("secret://kubernetes/%s/%s/%s", firstNonEmpty(deployment.Metadata.Namespace, "default"), secretName, key),
			})
			c.warn("KUBE_SECRET_REFERENCE_IMPORTED", fmt.Sprintf("%s.spec.template.spec.containers[0].env[%d].valueFrom.secretKeyRef", objectPath(deployment), i), "secretKeyRef was converted to a Skiff secret reference; plaintext secret values were not imported")
			continue
		}
		if configRef := mapAt(valueFrom, "configMapKeyRef"); len(configRef) > 0 {
			cmName := stringAt(configRef, "name")
			key := stringAt(configRef, "key")
			if value, ok := c.configMaps[namespacedKey(deployment.Metadata.Namespace, cmName)][key]; ok {
				runtimeEnv[name] = value
			} else {
				c.warn("KUBE_CONFIGMAP_VALUE_UNRESOLVED", fmt.Sprintf("%s.spec.template.spec.containers[0].env[%d].valueFrom.configMapKeyRef", objectPath(deployment), i), "ConfigMap key was not present in the imported manifest; set the environment value manually")
			}
		}
	}
	if len(mapList(container["envFrom"])) > 0 {
		c.warn("KUBE_ENVFROM_REVIEW_REQUIRED", objectPath(deployment)+".spec.template.spec.containers[0].envFrom", "envFrom is not expanded automatically; convert required keys into explicit env or secrets")
	}
	if len(runtimeEnv) == 0 {
		runtimeEnv = nil
	}
	sort.Slice(secrets, func(i, j int) bool { return secrets[i].Name < secrets[j].Name })
	return runtimeEnv, secrets
}

func (c *conversionContext) applyIngress(doc *spec.Document, ingress Object) {
	specMap := mapAt(ingress.Raw, "spec")
	rules := mapList(specMap["rules"])
	host := ""
	if len(rules) > 0 {
		host = stringAt(rules[0], "host")
	}
	tlsItems := mapList(specMap["tls"])
	certRef := ""
	if len(tlsItems) > 0 {
		secretName := stringAt(tlsItems[0], "secretName")
		if secretName != "" {
			certRef = fmt.Sprintf("secret://kubernetes/%s/%s/tls", firstNonEmpty(ingress.Metadata.Namespace, "default"), secretName)
		}
	}
	if host == "" {
		c.warn("KUBE_INGRESS_HOST_MISSING", objectPath(ingress), "Ingress has no host; generated Skiff service stays private")
		return
	}
	if certRef == "" {
		c.warn("KUBE_INGRESS_TLS_REQUIRED", objectPath(ingress)+".spec.tls", "Ingress has no TLS secret; generated Skiff service stays private until a certificate reference is configured")
		return
	}
	doc.Network.Ingress = &spec.Ingress{
		Type: "public-http",
		Host: host,
		TLS:  &spec.TLS{Enabled: true, CertRef: certRef},
	}
}

func (c *conversionContext) finish(doc spec.Document) (*Result, error) {
	var skiffYAML string
	var validation spec.Result
	if doc.APIVersion != "" {
		validation = spec.Validate(doc)
		if !validation.OK {
			for _, diagnostic := range validation.Diagnostics {
				c.error("SKIFF_SPEC_INVALID", diagnostic.Path, diagnostic.Code+": "+diagnostic.Message)
			}
		}
		body, err := spec.MarshalYAML(doc)
		if err != nil {
			return nil, err
		}
		skiffYAML = string(body)
	}
	ok := !hasError(c.findings)
	result := &Result{
		OK:        ok,
		Service:   doc,
		SkiffYAML: skiffYAML,
		Imported:  c.imported,
		Findings:  c.findings,
	}
	result.MarkdownReport = renderMarkdown(*result)
	return result, nil
}

func (c *conversionContext) warn(code, path, summary string) {
	c.findings = append(c.findings, Finding{Severity: "warn", Code: code, Path: path, Summary: summary})
}

func (c *conversionContext) error(code, path, summary string) {
	c.findings = append(c.findings, Finding{Severity: "error", Code: code, Path: path, Summary: summary})
}

func renderMarkdown(result Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Kubernetes Migration Report\n\n")
	if result.Service.Metadata.Name != "" {
		fmt.Fprintf(&b, "Service: `%s/%s`\n\n", result.Service.Metadata.Env, result.Service.Metadata.Name)
	}
	fmt.Fprintln(&b, "## Imported")
	for _, item := range result.Imported {
		name := item.Name
		if item.Namespace != "" {
			name = item.Namespace + "/" + name
		}
		fmt.Fprintf(&b, "- `%s` `%s`: %s\n", item.Kind, name, item.Action)
	}
	if len(result.Findings) > 0 {
		fmt.Fprintln(&b, "\n## Findings")
		for _, finding := range result.Findings {
			path := finding.Path
			if path != "" {
				path = " `" + path + "`"
			}
			fmt.Fprintf(&b, "- %s `%s`%s: %s\n", finding.Severity, finding.Code, path, finding.Summary)
		}
	}
	if result.SkiffYAML != "" {
		fmt.Fprintln(&b, "\n## Generated Skiff Spec")
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "```yaml")
		fmt.Fprint(&b, result.SkiffYAML)
		fmt.Fprintln(&b, "```")
	}
	return b.String()
}

func actionForKind(kind string) string {
	switch kind {
	case "Deployment":
		return "converted to Skiff Service"
	case "StatefulSet":
		return "converted to Skiff StatefulGroup proposal"
	case "Service":
		return "mapped to runtime port and target group"
	case "Ingress":
		return "mapped to network.ingress when TLS is present"
	case "HorizontalPodAutoscaler":
		return "mapped to scale min/max"
	case "ConfigMap":
		return "used for explicit configMapKeyRef values"
	case "Secret":
		return "referenced only; plaintext secret data was not imported"
	case "PodDisruptionBudget":
		return "reported for rollout review"
	default:
		return "reported"
	}
}

func ingressReferencesService(ingress Object, serviceName string) bool {
	specMap := mapAt(ingress.Raw, "spec")
	for _, rule := range mapList(specMap["rules"]) {
		http := mapAt(rule, "http")
		for _, path := range mapList(http["paths"]) {
			backend := mapAt(path, "backend")
			service := mapAt(backend, "service")
			if stringAt(service, "name") == serviceName {
				return true
			}
		}
	}
	defaultBackend := mapAt(specMap, "defaultBackend")
	return stringAt(mapAt(defaultBackend, "service"), "name") == serviceName
}

func selectorMatches(selector, labels map[string]string) bool {
	if len(selector) == 0 {
		return false
	}
	for key, value := range selector {
		if labels[key] != value {
			return false
		}
	}
	return true
}

func mapAt(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	if typed, ok := m[key].(map[string]any); ok {
		return typed
	}
	return nil
}

func mapList(value any) []map[string]any {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if typed, ok := item.(map[string]any); ok {
			out = append(out, typed)
		}
	}
	return out
}

func stringMap(value any) map[string]string {
	typed, ok := value.(map[string]any)
	if !ok || len(typed) == 0 {
		return nil
	}
	out := make(map[string]string, len(typed))
	for key, value := range typed {
		if s, ok := value.(string); ok {
			out[key] = s
		}
	}
	return out
}

func stringAt(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if value, ok := m[key].(string); ok {
		return value
	}
	return ""
}

func intAt(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	return intValue(m[key])
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		return 0
	default:
		return 0
	}
}

func boolAt(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	value, _ := m[key].(bool)
	return value
}

func stringSlice(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func namespacedName(object Object) string {
	return namespacedKey(object.Metadata.Namespace, object.Metadata.Name)
}

func namespacedKey(namespace, name string) string {
	return firstNonEmpty(namespace, "default") + "/" + name
}

func objectPath(object Object) string {
	name := object.Metadata.Name
	if object.Metadata.Namespace != "" {
		name = object.Metadata.Namespace + "/" + name
	}
	return object.Kind + "/" + name
}

func appendSecretRef(values []spec.SecretRef, value spec.SecretRef) []spec.SecretRef {
	if value.Name == "" || value.Ref == "" {
		return values
	}
	for _, existing := range values {
		if existing.Name == value.Name || existing.Ref == value.Ref {
			return values
		}
	}
	return append(values, value)
}

func dnsName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "secret"
	}
	if len(out) > 63 {
		out = strings.Trim(out[:63], "-")
	}
	return out
}

func nonEmptyStrings(values []string) []string {
	var out []string
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	if len(out) == 0 {
		return []string{"unnamed"}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func hasError(findings []Finding) bool {
	for _, finding := range findings {
		if finding.Severity == "error" {
			return true
		}
	}
	return false
}

func isProductionEnv(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "prod", "production":
		return true
	default:
		return false
	}
}

func isServiceMeshAnnotation(key string) bool {
	key = strings.ToLower(key)
	return strings.Contains(key, "istio.io/") ||
		strings.Contains(key, "linkerd.io/") ||
		strings.Contains(key, "consul.hashicorp.com/") ||
		strings.Contains(key, "sidecar")
}

func isSkiffVolumeType(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "gp3", "io1", "standard":
		return true
	default:
		return false
	}
}
