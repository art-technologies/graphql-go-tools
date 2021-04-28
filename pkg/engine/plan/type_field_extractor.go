package plan

import (
	"github.com/jensneuse/graphql-go-tools/pkg/ast"
	"github.com/jensneuse/graphql-go-tools/pkg/astparser"
	"github.com/jensneuse/graphql-go-tools/pkg/astprinter"
	"github.com/jensneuse/graphql-go-tools/pkg/asttransform"
	"github.com/jensneuse/graphql-go-tools/pkg/federation"
)

const (
	federationKeyDirectiveName      = "key"
	federationRequireDirectiveName  = "requires"
	federationExternalDirectiveName = "external"
)

// TypeFieldExtractor takes an ast.Document as input
// and generates the TypeField configuration for both root fields & child fields
// If a type is a federation entity (annotated with @key directive)
// and a field is is extended, this field will be skipped
// so that only "local" fields will be generated
type TypeFieldExtractor struct {
	document *ast.Document
}

func NewNodeExtractor(document *ast.Document) *TypeFieldExtractor {
	return &TypeFieldExtractor{document: document}
}

// GetAllNodes returns all Root- & ChildNodes
func (r *TypeFieldExtractor) GetAllNodes() (rootNodes, childNodes []TypeField) {
	rootNodes = r.getAllRootNodes()
	childNodes = r.getAllChildNodes(rootNodes)
	return
}

func (r *TypeFieldExtractor) getAllRootNodes() []TypeField {
	var rootNodes []TypeField

	for _, astNode := range r.document.RootNodes {
		switch astNode.Kind {
		case ast.NodeKindObjectTypeExtension, ast.NodeKindObjectTypeDefinition:
			r.addRootNodes(astNode, &rootNodes)
		}
	}

	return rootNodes
}

func (r *TypeFieldExtractor) getAllChildNodes(rootNodes []TypeField) []TypeField {
	var childNodes []TypeField

	for i := range rootNodes {
		fieldNameToRef := make(map[string]int, len(rootNodes[i].FieldNames))

		rootNodeASTNode, exists := r.document.Index.FirstNodeByNameStr(rootNodes[i].TypeName)
		if !exists {
			continue
		}

		fieldRefs := r.document.NodeFieldDefinitions(rootNodeASTNode)
		for _, fieldRef := range fieldRefs {
			fieldName := r.document.FieldDefinitionNameString(fieldRef)
			fieldNameToRef[fieldName] = fieldRef
		}

		for _, fieldName := range rootNodes[i].FieldNames {
			fieldRef := fieldNameToRef[fieldName]

			fieldTypeName := r.document.NodeNameString(r.document.FieldDefinitionTypeNode(fieldRef))
			r.findChildNodesForType(fieldTypeName, &childNodes)
		}
	}

	return childNodes
}

func (r *TypeFieldExtractor) findChildNodesForType(typeName string, childNodes *[]TypeField) {
	node, exists := r.document.Index.FirstNodeByNameStr(typeName)
	if !exists {
		return
	}

	fieldsRefs := r.document.NodeFieldDefinitions(node)

	for _, fieldRef := range fieldsRefs {
		fieldName := r.document.FieldDefinitionNameString(fieldRef)

		if added := r.addChildTypeFieldName(typeName, fieldName, childNodes); !added {
			continue
		}

		fieldTypeName := r.document.NodeNameString(r.document.FieldDefinitionTypeNode(fieldRef))
		r.findChildNodesForType(fieldTypeName, childNodes)
	}
}

func (r *TypeFieldExtractor) addChildTypeFieldName(typeName, fieldName string, childNodes *[]TypeField) bool {
	for i := range *childNodes {
		if (*childNodes)[i].TypeName != typeName {
			continue
		}

		for _, field := range (*childNodes)[i].FieldNames {
			if field == fieldName {
				return false
			}
		}

		(*childNodes)[i].FieldNames = append((*childNodes)[i].FieldNames, fieldName)
		return true
	}

	*childNodes = append(*childNodes, TypeField{
		TypeName:   typeName,
		FieldNames: []string{fieldName},
	})

	return true
}

func (r *TypeFieldExtractor) addRootNodes(astNode ast.Node, rootNodes *[]TypeField) {
	typeName := r.document.NodeNameString(astNode)

	// we need to first build the base schema so that we get a valid Index
	// to look up if typeName is a RootOperationTypeName
	// the service SDL itself might use ObjectTypeExtension types which will not be indexed
	document := r.baseSchema()

	// node should be an entity or a root operation type definition
	// if document == nil, there are no root operation type definitions in this document
	if !r.isEntity(astNode) && (document == nil || !document.Index.IsRootOperationTypeNameString(typeName)) {
		return
	}

	var fieldNames []string

	fieldRefs := r.document.NodeFieldDefinitions(astNode)
	for _, fieldRef := range fieldRefs {
		// check if field definition is external (has external directive)
		if r.document.FieldDefinitionHasNamedDirective(fieldRef,federationExternalDirectiveName) {
			continue
		}

		fieldName := r.document.FieldDefinitionNameString(fieldRef)
		fieldNames = append(fieldNames, fieldName)
	}

	if len(fieldNames) == 0 {
		return
	}

	*rootNodes = append(*rootNodes, TypeField{
		TypeName:   typeName,
		FieldNames: fieldNames,
	})
}

func (r *TypeFieldExtractor) baseSchema () *ast.Document {
	schemaSDL,err := astprinter.PrintString(r.document,nil)
	if err != nil {
		return nil
	}
	baseSchemaSDL,err := federation.BuildBaseSchemaDocument(schemaSDL)
	if err != nil {
		return nil
	}
	document,report := astparser.ParseGraphqlDocumentString(baseSchemaSDL)
	if report.HasErrors() {
		return nil
	}
	err = asttransform.MergeDefinitionWithBaseSchema(&document)
	if err != nil {
		return nil
	}
	mergedSDL,err := astprinter.PrintString(&document,nil)
	if err != nil {
		return nil
	}
	mergedDocument,report := astparser.ParseGraphqlDocumentString(mergedSDL)
	if report.HasErrors() {
		return nil
	}
	return &mergedDocument
}

func (r *TypeFieldExtractor) isEntity(astNode ast.Node) bool {
	directiveRefs := r.document.NodeDirectives(astNode)

	for _, directiveRef := range directiveRefs {
		if directiveName := r.document.DirectiveNameString(directiveRef); directiveName == federationKeyDirectiveName {
			return true
		}
	}

	return false
}
