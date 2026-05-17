package compiler

import (
	"fmt"
	"strings"

	"github.com/s1liconcow/skiff/internal/ir"
	"github.com/s1liconcow/skiff/internal/spec"
)

func compileStack(doc spec.Document, opts Options) (*ir.Graph, error) {
	if doc.Stack == nil || len(doc.Stack.Services) != 1 || len(doc.Stack.Databases) != 1 {
		return nil, fmt.Errorf("stack compiler supports one service and one managed database")
	}

	stackName := doc.Metadata.Name
	env := doc.Metadata.Env
	serviceSpec := doc.Stack.Services[0]
	databaseSpec := doc.Stack.Databases[0]
	serviceName := stackComponentName(stackName, serviceSpec.Name)
	databaseName := stackComponentName(stackName, databaseSpec.Name)

	bindings := bindingsForService(doc.Stack.Bindings, serviceSpec.Name, databaseSpec.Name, databaseName)
	serviceDoc := spec.Document{
		APIVersion: doc.APIVersion,
		Kind:       spec.KindService,
		Metadata: spec.Metadata{
			Name:   serviceName,
			Env:    env,
			Labels: cloneMap(doc.Metadata.Labels),
		},
		Artifact: serviceSpec.Artifact,
		Runtime:  serviceSpec.Runtime,
		Machine:  serviceSpec.Machine,
		Scale:    serviceSpec.Scale,
		Network:  serviceSpec.Network,
		Rollout:  serviceSpec.Rollout,
		Secrets:  append([]spec.SecretRef(nil), serviceSpec.Secrets...),
	}
	for _, binding := range bindings {
		serviceDoc.Secrets = append(serviceDoc.Secrets, spec.SecretRef{Name: envSecretName(binding.EnvName), Ref: binding.SecretRef})
		if serviceDoc.Runtime.Env == nil {
			serviceDoc.Runtime.Env = map[string]string{}
		}
		serviceDoc.Runtime.Env[binding.EnvName] = binding.SecretRef
	}

	graph := compileService(serviceDoc, opts)
	graph.Resources.SecurityGroups = append(graph.Resources.SecurityGroups, databaseSecurityGroup(env, serviceName, databaseName, databasePort(databaseSpec.Engine)))
	addDatabaseEgress(graph, databaseName, databasePort(databaseSpec.Engine))

	secretRef := ""
	if len(bindings) > 0 {
		secretRef = bindings[0].SecretRef
	}
	dbMeta := meta("managed-database:"+databaseName, ir.ResourceKindManagedDatabase, resourceName(env, databaseName, "db"), databaseTags(serviceName, env, databaseName), "$.stack.databases[0]")
	graph.Resources.ManagedDatabases = []ir.ManagedDatabase{{
		Meta:                dbMeta,
		Engine:              normalizeDatabaseEngine(databaseSpec.Engine),
		Version:             databaseSpec.Version,
		Size:                databaseSpec.Size,
		Port:                databasePort(databaseSpec.Engine),
		Region:              firstNonEmpty(databaseSpec.Region, doc.Metadata.Labels["region"]),
		Storage:             compileDatabaseStorage(databaseSpec.Storage),
		Backups:             compileDatabaseBackups(databaseSpec.Backups),
		Network:             compileDatabaseNetwork(databaseSpec.Network),
		SecurityGroupRefs:   []string{"security-group:" + databaseName},
		ConnectionSecretRef: secretRef,
	}}
	for _, binding := range bindings {
		graph.Resources.DatabaseSecrets = append(graph.Resources.DatabaseSecrets, ir.DatabaseSecret{
			Meta:        meta("database-secret:"+databaseName+":"+envSecretName(binding.EnvName), ir.ResourceKindDatabaseSecret, resourceName(env, databaseName, envSecretName(binding.EnvName)+"-secret"), databaseTags(serviceName, env, databaseName), "$.stack.bindings"),
			DatabaseRef: "managed-database:" + databaseName,
			Name:        envSecretName(binding.EnvName),
			Ref:         binding.SecretRef,
			EnvName:     binding.EnvName,
		})
		graph.Resources.DatabaseBindings = append(graph.Resources.DatabaseBindings, ir.DatabaseBinding{
			Meta:        meta("database-binding:"+serviceName+":"+databaseName+":"+binding.EnvName, ir.ResourceKindDatabaseBinding, resourceName(env, serviceName, strings.ToLower(binding.EnvName)+"-binding"), databaseTags(serviceName, env, databaseName), "$.stack.bindings"),
			FromService: serviceName,
			DatabaseRef: "managed-database:" + databaseName,
			EnvName:     binding.EnvName,
			SecretRef:   binding.SecretRef,
		})
	}
	return graph, nil
}

type compiledBinding struct {
	EnvName   string
	SecretRef string
}

func bindingsForService(bindings []spec.StackBinding, serviceComponent, databaseComponent, databaseName string) []compiledBinding {
	var out []compiledBinding
	for _, binding := range bindings {
		if binding.From != serviceComponent || binding.To != databaseComponent {
			continue
		}
		out = append(out, compiledBinding{
			EnvName:   binding.As,
			SecretRef: databaseConnectionSecretRef(databaseName),
		})
	}
	return out
}

func databaseSecurityGroup(env, serviceName, databaseName string, port int) ir.SecurityGroup {
	return ir.SecurityGroup{
		Meta: meta("security-group:"+databaseName, ir.ResourceKindSecurityGroup, resourceName(env, databaseName, "db-sg"), databaseTags(serviceName, env, databaseName), "$.stack.databases[0].network", "$.stack.bindings"),
		Rules: []ir.SecurityRule{
			{
				Direction:   "ingress",
				Protocol:    "tcp",
				FromPort:    port,
				ToPort:      port,
				Source:      "security-group:" + serviceName,
				Description: "allow bound service traffic to managed database",
			},
			{
				Direction:   "egress",
				Protocol:    "all",
				Destination: "0.0.0.0/0",
				Description: "allow managed database control-plane egress",
			},
		},
	}
}

func addDatabaseEgress(graph *ir.Graph, databaseName string, port int) {
	destination := "security-group:" + databaseName
	for i := range graph.Resources.SecurityGroups {
		if graph.Resources.SecurityGroups[i].Meta.LogicalID != resourceIDs(graph.Service).securityGroup {
			continue
		}
		graph.Resources.SecurityGroups[i].Rules = append(graph.Resources.SecurityGroups[i].Rules, ir.SecurityRule{
			Direction:   "egress",
			Protocol:    "tcp",
			FromPort:    port,
			ToPort:      port,
			Destination: destination,
			Description: "allow service traffic to managed database",
		})
		return
	}
}

func stackComponentName(stackName, component string) string {
	component = strings.TrimSpace(component)
	if strings.TrimSpace(stackName) == "" || strings.HasPrefix(component, stackName+"-") {
		return component
	}
	return stackName + "-" + component
}

func databaseConnectionSecretRef(databaseName string) string {
	return "secret://managed-database/" + databaseName + "/connection-url"
}

func envSecretName(envName string) string {
	name := strings.ToLower(strings.TrimSpace(envName))
	name = strings.ReplaceAll(name, "_", "-")
	if name == "" {
		return "database-url"
	}
	return name
}

func databaseTags(service, env, database string) map[string]string {
	tags := ir.RequiredTags(service, env)
	tags[ir.TagDatabase] = database
	return tags
}

func compileDatabaseStorage(in spec.DatabaseStorage) ir.DatabaseStorage {
	return ir.DatabaseStorage{SizeGB: in.SizeGB, Type: in.Type, Encrypted: in.Encrypted}
}

func compileDatabaseBackups(in spec.DatabaseBackups) ir.DatabaseBackups {
	return ir.DatabaseBackups{Enabled: in.Enabled, RetentionDays: in.RetentionDays, Window: in.Window}
}

func compileDatabaseNetwork(in spec.DatabaseNetwork) ir.DatabaseNetwork {
	return ir.DatabaseNetwork{
		Private:           in.Private,
		SubnetGroupRef:    in.SubnetGroupRef,
		SecurityGroupRefs: append([]string(nil), in.SecurityGroupRefs...),
	}
}

func normalizeDatabaseEngine(engine string) string {
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case "postgresql":
		return "postgres"
	default:
		return strings.ToLower(strings.TrimSpace(engine))
	}
}

func databasePort(engine string) int {
	switch normalizeDatabaseEngine(engine) {
	case "mysql", "aurora-mysql":
		return 3306
	default:
		return 5432
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
