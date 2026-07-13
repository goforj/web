package webindex

// collectSchemas projects the registry graph into the versioned manifest without shape-merging distinct Go identities.
func collectSchemas(registry *typedSchemaRegistry) []Schema {
	components := registry.componentSnapshot()
	if len(components) == 0 {
		return nil
	}
	out := make([]Schema, 0, len(components))
	for _, component := range components {
		out = append(out, Schema{
			Identity:   component.Identity,
			Name:       component.Name,
			Package:    component.Package,
			TypeName:   component.TypeName,
			Definition: component.Schema,
			Confidence: component.Confidence,
		})
	}
	return out
}
