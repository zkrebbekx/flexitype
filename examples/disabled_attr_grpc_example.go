package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bufbuild/connect-go"

	"github.com/zac300/flexitype/api/flexitypev1"
	"github.com/zac300/flexitype/api/flexitypev1connect"
	"github.com/zac300/flexitype/pkg/sdk"
)

// This example demonstrates the attribute disabling feature using gRPC
func main() {
	// Configure server address
	serverAddr := "http://localhost:8080"
	if envAddr := os.Getenv("FLEXITYPE_SERVER"); envAddr != "" {
		serverAddr = envAddr
	}

	// Create a Connect client
	client := flexitypev1connect.NewFlexiTypeServiceClient(
		http.DefaultClient,
		serverAddr,
	)

	ctx := context.Background()

	// Start server in a separate goroutine if needed
	// For this example, we assume a server is running
	// You could start the server here using the same code from cmd/server/main.go

	// Handle graceful shutdown
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-done
		fmt.Println("\nShutting down...")
		os.Exit(0)
	}()

	// Step 1: Create a type definition
	fmt.Println("=== Step 1: Creating a type definition ===")

	createTypeReq := connect.NewRequest(&flexitypev1.SaveTypeRequest{
		Id:          "doc-type-001",
		Name:        "Document",
		Description: "A document type with attributes",
	})

	createTypeRes, err := client.SaveType(ctx, createTypeReq)
	if err != nil {
		log.Fatalf("Failed to create type: %v", err)
	}

	typeDef := createTypeRes.Msg.Type
	fmt.Printf("Created type: %s (v%d)\n", typeDef.Name, typeDef.Version)

	// Step 2: Add attributes to the type
	fmt.Println("\n=== Step 2: Adding attributes to the type ===")

	// Add title attribute
	titleAttrReq := connect.NewRequest(&flexitypev1.AddAttributeRequest{
		TypeId: typeDef.Id,
		Attribute: &flexitypev1.AttributeDefinition{
			Id:          "attr-001",
			Name:        "title",
			Description: "Document title",
			DataType:    sdk.DataTypeString,
			Required:    true,
			ValidationRules: []*flexitypev1.ValidationRule{
				{
					Type:       "required",
					Parameters: nil,
				},
			},
		},
	})

	_, err = client.AddAttribute(ctx, titleAttrReq)
	if err != nil {
		log.Fatalf("Failed to add title attribute: %v", err)
	}

	// Add author attribute
	authorAttrReq := connect.NewRequest(&flexitypev1.AddAttributeRequest{
		TypeId: typeDef.Id,
		Attribute: &flexitypev1.AttributeDefinition{
			Id:          "attr-002",
			Name:        "author",
			Description: "Document author",
			DataType:    sdk.DataTypeString,
			Required:    false,
		},
	})

	_, err = client.AddAttribute(ctx, authorAttrReq)
	if err != nil {
		log.Fatalf("Failed to add author attribute: %v", err)
	}

	// Add content attribute
	contentAttrReq := connect.NewRequest(&flexitypev1.AddAttributeRequest{
		TypeId: typeDef.Id,
		Attribute: &flexitypev1.AttributeDefinition{
			Id:          "attr-003",
			Name:        "content",
			Description: "Document content",
			DataType:    sdk.DataTypeString,
			Required:    true,
			ValidationRules: []*flexitypev1.ValidationRule{
				{
					Type:       "required",
					Parameters: nil,
				},
			},
		},
	})

	contentAttrRes, err := client.AddAttribute(ctx, contentAttrReq)
	if err != nil {
		log.Fatalf("Failed to add content attribute: %v", err)
	}

	// Get the updated type
	typeDef = contentAttrRes.Msg.Type

	fmt.Printf("Added attributes to type (now v%d):\n", typeDef.Version)
	for _, attr := range typeDef.Attributes {
		fmt.Printf("  - %s (%s)\n", attr.Name, attr.Id)
	}

	// Step 3: Create an instance of the type
	fmt.Println("\n=== Step 3: Creating an instance of the type ===")

	// Set attribute values
	attrValues := make(map[string]*flexitypev1.AttributeValue)

	// Add title
	attrValues["title"] = &flexitypev1.AttributeValue{
		Value: &flexitypev1.AttributeValue_StringValue{
			StringValue: "Sample Document",
		},
	}

	// Add author
	attrValues["author"] = &flexitypev1.AttributeValue{
		Value: &flexitypev1.AttributeValue_StringValue{
			StringValue: "John Doe",
		},
	}

	// Add content
	attrValues["content"] = &flexitypev1.AttributeValue{
		Value: &flexitypev1.AttributeValue_StringValue{
			StringValue: "This is a sample document content.",
		},
	}

	createInstanceReq := connect.NewRequest(&flexitypev1.SaveInstanceRequest{
		Id:              "doc-001",
		TypeId:          typeDef.Id,
		AttributeValues: attrValues,
	})

	createInstanceRes, err := client.SaveInstance(ctx, createInstanceReq)
	if err != nil {
		log.Fatalf("Failed to create instance: %v", err)
	}

	instance := createInstanceRes.Msg.Instance
	fmt.Printf("Created instance: %s (type v%d)\n", instance.Id, instance.TypeVersion)
	for attrId, attrValue := range instance.AttributeValues {
		var value interface{}

		// Check which oneOf value is set
		switch v := attrValue.Value.(type) {
		case *flexitypev1.AttributeValue_StringValue:
			value = v.StringValue
		case *flexitypev1.AttributeValue_IntValue:
			value = v.IntValue
		case *flexitypev1.AttributeValue_FloatValue:
			value = v.FloatValue
		case *flexitypev1.AttributeValue_BoolValue:
			value = v.BoolValue
		case *flexitypev1.AttributeValue_DateValue:
			value = v.DateValue
		case *flexitypev1.AttributeValue_ObjectValue:
			value = "(object)"
		case *flexitypev1.AttributeValue_ArrayValue:
			value = "(array)"
		}

		fmt.Printf("  %s = %v\n", attrId, value)
	}

	// Step 4: Disable the author attribute
	fmt.Println("\n=== Step 4: Disabling the author attribute ===")

	disableAttrReq := connect.NewRequest(&flexitypev1.SetAttributeDisabledStateRequest{
		TypeId:        typeDef.Id,
		AttributeName: "author",
		Disabled:      true,
	})

	disableAttrRes, err := client.SetAttributeDisabledState(ctx, disableAttrReq)
	if err != nil {
		log.Fatalf("Failed to disable attribute: %v", err)
	}

	typeDef = disableAttrRes.Msg.Type
	fmt.Printf("Disabled author attribute in type version %d\n", typeDef.Version)

	// Find the author attribute and check its state
	for _, attr := range typeDef.Attributes {
		if attr.Name == "author" {
			fmt.Printf("  Attribute '%s' disabled state: %v\n", attr.Name, attr.Disabled)
		}
	}

	// Step 5: Try to update the instance with the disabled attribute
	fmt.Println("\n=== Step 5: Trying to update instance with disabled attribute ===")

	updateInstValues := make(map[string]*flexitypev1.AttributeValue)
	updateInstValues["author"] = &flexitypev1.AttributeValue{
		Value: &flexitypev1.AttributeValue_StringValue{
			StringValue: "Jane Smith", // This should fail because author is disabled
		},
	}

	updateInstReq := connect.NewRequest(&flexitypev1.UpdateInstanceRequest{
		Id:              instance.Id,
		AttributeValues: updateInstValues,
	})

	_, err = client.UpdateInstance(ctx, updateInstReq)
	if err != nil {
		fmt.Printf("Got expected error: %v\n", err)
	} else {
		fmt.Println("Warning: Expected update to fail because author attribute is disabled")
	}

	// Step 6: Update a valid attribute
	fmt.Println("\n=== Step 6: Updating instance with valid attribute ===")

	updateValidValues := make(map[string]*flexitypev1.AttributeValue)
	updateValidValues["title"] = &flexitypev1.AttributeValue{
		Value: &flexitypev1.AttributeValue_StringValue{
			StringValue: "Updated Document Title",
		},
	}

	updateValidReq := connect.NewRequest(&flexitypev1.UpdateInstanceRequest{
		Id:              instance.Id,
		AttributeValues: updateValidValues,
	})

	updateValidRes, err := client.UpdateInstance(ctx, updateValidReq)
	if err != nil {
		log.Fatalf("Failed to update instance: %v", err)
	}

	updatedInstance := updateValidRes.Msg.Instance
	fmt.Printf("Updated instance: %s (type v%d)\n", updatedInstance.Id, updatedInstance.TypeVersion)
	for attrId, attrValue := range updatedInstance.AttributeValues {
		var value interface{}

		// Check which oneOf value is set
		switch v := attrValue.Value.(type) {
		case *flexitypev1.AttributeValue_StringValue:
			value = v.StringValue
		case *flexitypev1.AttributeValue_IntValue:
			value = v.IntValue
		case *flexitypev1.AttributeValue_FloatValue:
			value = v.FloatValue
		case *flexitypev1.AttributeValue_BoolValue:
			value = v.BoolValue
		case *flexitypev1.AttributeValue_DateValue:
			value = v.DateValue
		case *flexitypev1.AttributeValue_ObjectValue:
			value = "(object)"
		case *flexitypev1.AttributeValue_ArrayValue:
			value = "(array)"
		}

		fmt.Printf("  %s = %v\n", attrId, value)
	}

	// Step 7: Create a new instance with the updated type definition (with disabled attribute)
	fmt.Println("\n=== Step 7: Creating a new instance with updated type definition ===")

	// Try to create with the disabled attribute
	createWithDisabledValues := make(map[string]*flexitypev1.AttributeValue)

	createWithDisabledValues["title"] = &flexitypev1.AttributeValue{
		Value: &flexitypev1.AttributeValue_StringValue{
			StringValue: "New Document",
		},
	}

	createWithDisabledValues["author"] = &flexitypev1.AttributeValue{
		Value: &flexitypev1.AttributeValue_StringValue{
			StringValue: "Jane Smith", // This should fail because author is disabled
		},
	}

	createWithDisabledValues["content"] = &flexitypev1.AttributeValue{
		Value: &flexitypev1.AttributeValue_StringValue{
			StringValue: "This is a new document content.",
		},
	}

	createWithDisabledReq := connect.NewRequest(&flexitypev1.SaveInstanceRequest{
		Id:              "doc-002",
		TypeId:          typeDef.Id,
		AttributeValues: createWithDisabledValues,
	})

	_, err = client.SaveInstance(ctx, createWithDisabledReq)
	if err != nil {
		fmt.Printf("Got expected error: %v\n", err)
	} else {
		fmt.Println("Warning: Expected creation to fail because author attribute is disabled")
	}

	// Create without the disabled attribute
	createValidValues := make(map[string]*flexitypev1.AttributeValue)

	createValidValues["title"] = &flexitypev1.AttributeValue{
		Value: &flexitypev1.AttributeValue_StringValue{
			StringValue: "New Document",
		},
	}

	createValidValues["content"] = &flexitypev1.AttributeValue{
		Value: &flexitypev1.AttributeValue_StringValue{
			StringValue: "This is a new document content.",
		},
	}

	createValidReq := connect.NewRequest(&flexitypev1.SaveInstanceRequest{
		Id:              "doc-002",
		TypeId:          typeDef.Id,
		AttributeValues: createValidValues,
	})

	createValidRes, err := client.SaveInstance(ctx, createValidReq)
	if err != nil {
		log.Fatalf("Failed to create instance: %v", err)
	}

	newInstance := createValidRes.Msg.Instance
	fmt.Printf("Created new instance: %s (type v%d)\n", newInstance.Id, newInstance.TypeVersion)
	for attrId, attrValue := range newInstance.AttributeValues {
		var value interface{}

		// Check which oneOf value is set
		switch v := attrValue.Value.(type) {
		case *flexitypev1.AttributeValue_StringValue:
			value = v.StringValue
		case *flexitypev1.AttributeValue_IntValue:
			value = v.IntValue
		case *flexitypev1.AttributeValue_FloatValue:
			value = v.FloatValue
		case *flexitypev1.AttributeValue_BoolValue:
			value = v.BoolValue
		case *flexitypev1.AttributeValue_DateValue:
			value = v.DateValue
		case *flexitypev1.AttributeValue_ObjectValue:
			value = "(object)"
		case *flexitypev1.AttributeValue_ArrayValue:
			value = "(array)"
		}

		fmt.Printf("  %s = %v\n", attrId, value)
	}

	// Step 8: Re-enable the author attribute
	fmt.Println("\n=== Step 8: Re-enabling the author attribute ===")

	enableAttrReq := connect.NewRequest(&flexitypev1.SetAttributeDisabledStateRequest{
		TypeId:        typeDef.Id,
		AttributeName: "author",
		Disabled:      false,
	})

	enableAttrRes, err := client.SetAttributeDisabledState(ctx, enableAttrReq)
	if err != nil {
		log.Fatalf("Failed to re-enable attribute: %v", err)
	}

	typeDef = enableAttrRes.Msg.Type
	fmt.Printf("Re-enabled author attribute in type version %d\n", typeDef.Version)

	// Step 9: Update the instance with the re-enabled attribute
	fmt.Println("\n=== Step 9: Updating instance with re-enabled attribute ===")

	updateReenabledValues := make(map[string]*flexitypev1.AttributeValue)
	updateReenabledValues["author"] = &flexitypev1.AttributeValue{
		Value: &flexitypev1.AttributeValue_StringValue{
			StringValue: "Jane Smith", // This should work now
		},
	}

	updateReenabledReq := connect.NewRequest(&flexitypev1.UpdateInstanceRequest{
		Id:              newInstance.Id,
		AttributeValues: updateReenabledValues,
	})

	updateReenabledRes, err := client.UpdateInstance(ctx, updateReenabledReq)
	if err != nil {
		log.Fatalf("Failed to update instance: %v", err)
	}

	updatedInstance = updateReenabledRes.Msg.Instance
	fmt.Printf("Updated instance: %s (type v%d)\n", updatedInstance.Id, updatedInstance.TypeVersion)
	for attrId, attrValue := range updatedInstance.AttributeValues {
		var value interface{}

		// Check which oneOf value is set
		switch v := attrValue.Value.(type) {
		case *flexitypev1.AttributeValue_StringValue:
			value = v.StringValue
		case *flexitypev1.AttributeValue_IntValue:
			value = v.IntValue
		case *flexitypev1.AttributeValue_FloatValue:
			value = v.FloatValue
		case *flexitypev1.AttributeValue_BoolValue:
			value = v.BoolValue
		case *flexitypev1.AttributeValue_DateValue:
			value = v.DateValue
		case *flexitypev1.AttributeValue_ObjectValue:
			value = "(object)"
		case *flexitypev1.AttributeValue_ArrayValue:
			value = "(array)"
		}

		fmt.Printf("  %s = %v\n", attrId, value)
	}

	// Step 10: Export the type schema to YAML via gRPC
	fmt.Println("\n=== Step 10: Exporting type schema to YAML via gRPC ===")

	exportReq := connect.NewRequest(&flexitypev1.ExportTypeSchemaRequest{
		TypeId: typeDef.Id,
	})

	exportRes, err := client.ExportTypeSchema(ctx, exportReq)
	if err != nil {
		log.Fatalf("Failed to export type schema: %v", err)
	}

	fmt.Printf("Exported YAML schema:\n%s\n", exportRes.Msg.YamlContent)

	// Wait a moment before exiting
	time.Sleep(100 * time.Millisecond)
	fmt.Println("\nExample completed successfully!")
}
