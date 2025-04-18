package main

import (
	"context"
	"fmt"
	"log"

	"github.com/zac300/flexitype/internal/adapters/repositories/memory"
	"github.com/zac300/flexitype/pkg/sdk"
)

func main() {
	// Initialize repositories
	typeRepo := memory.NewInMemoryTypeRepository()
	instanceRepo := memory.NewInMemoryInstanceRepository()

	// Create SDK client
	client := sdk.NewClient(typeRepo, instanceRepo)

	// Create context
	ctx := context.Background()

	// ---- STEP 1: Create a type definition with multiple attributes ----
	fmt.Println("=== Step 1: Creating a type definition with multiple attributes ===")

	// Create a document type
	docType, err := client.SaveType(ctx, "Document", "Base document type")
	if err != nil {
		log.Fatalf("Failed to create document type: %v", err)
	}

	// Add attributes
	titleAttr := sdk.NewAttribute("title", "Document title", sdk.StringType, true)
	titleAttr.AddValidationRule(&sdk.RequiredRule{})

	contentAttr := sdk.NewAttribute("content", "Document content", sdk.StringType, true)
	contentAttr.AddValidationRule(&sdk.RequiredRule{})

	authorAttr := sdk.NewAttribute("author", "Document author", sdk.StringType, false)

	// Version-control attributes
	versionAttr := sdk.NewAttribute("version", "Document version", sdk.StringType, true)
	versionAttr.SetDefaultValue("1.0")

	lastModifiedAttr := sdk.NewAttribute("lastModified", "Last modified date", sdk.StringType, true)
	lastModifiedAttr.SetDefaultValue("2023-01-01")

	// Add all attributes to the type
	client.AddAttribute(ctx, docType.Name, titleAttr)
	client.AddAttribute(ctx, docType.Name, contentAttr)
	client.AddAttribute(ctx, docType.Name, authorAttr)
	client.AddAttribute(ctx, docType.Name, versionAttr)
	client.AddAttribute(ctx, docType.Name, lastModifiedAttr)

	// Save the type
	err = typeRepo.Save(ctx, docType)
	if err != nil {
		log.Fatalf("Failed to save document type: %v", err)
	}

	fmt.Printf("Created document type v%d with %d attributes\n",
		docType.Version, len(docType.Attributes))

	// ---- STEP 2: Create an instance of the type ----
	fmt.Println("\n=== Step 2: Creating an instance with all attributes ===")

	docAttrs := map[string]interface{}{
		"title":        "Sample Document",
		"content":      "This is a sample document.",
		"author":       "John Doe",
		"version":      "1.0",
		"lastModified": "2023-01-01",
	}

	doc, err := client.SaveInstance(ctx, "doc-inst-001", docType, docAttrs)
	if err != nil {
		log.Fatalf("Failed to create document instance: %v", err)
	}

	fmt.Printf("Created document instance: %s\n", doc.ID)
	fmt.Printf("Type version: %d\n", doc.TypeVersion)
	for name, value := range doc.Attributes {
		fmt.Printf("  %s = %v\n", name, value)
	}

	// ---- STEP 3: Disable an attribute in a new type version ----
	fmt.Println("\n=== Step 3: Disabling the 'author' attribute in a new type version ===")

	// Get the type again
	docType, err = client.GetType(ctx, "doc-001")
	if err != nil {
		log.Fatalf("Failed to get document type: %v", err)
	}

	// Disable the author attribute
	updatedType, err := client.SetAttributeDisabledState(ctx, docType.Name, authorAttr.Name, true)
	if err != nil {
		log.Fatalf("Failed to disable attribute: %v", err)
	}

	fmt.Printf("Disabled 'author' attribute in type version %d\n", updatedType.Version)

	// ---- STEP 4: Update the instance (should ignore the disabled attribute) ----
	fmt.Println("\n=== Step 4: Updating the instance (should ignore the disabled attribute) ===")

	// Try to update the author attribute (should be ignored)
	updatedDoc, err := client.UpdateInstance(ctx, "doc-inst-001", map[string]interface{}{
		"title":  "Updated Document",
		"author": "Jane Smith", // This should be ignored or rejected
	})

	if err != nil {
		fmt.Printf("Expected error: %v\n", err)
	} else {
		fmt.Printf("Updated document instance with type version %d\n", updatedDoc.TypeVersion)
		fmt.Printf("This should have ignored the 'author' attribute\n")

		for name, value := range updatedDoc.Attributes {
			fmt.Printf("  %s = %v\n", name, value)
		}
	}

	// ---- STEP 5: Create a new instance of the updated type ----
	fmt.Println("\n=== Step 5: Creating a new instance with the updated type (v2) ===")

	// Try to create a doc with the author field
	newDocAttrs := map[string]interface{}{
		"title":        "New Document",
		"content":      "This is a new document.",
		"author":       "Will Smith", // This should be rejected
		"version":      "1.0",
		"lastModified": "2023-01-02",
	}

	_, err = client.SaveInstance(ctx, "doc-inst-002", updatedType, newDocAttrs)
	if err != nil {
		fmt.Printf("Expected error: %v\n", err)
	} else {
		fmt.Println("This should have failed since 'author' is disabled")
	}

	// Create without the author field
	newDocAttrs = map[string]interface{}{
		"title":        "New Document",
		"content":      "This is a new document.",
		"version":      "1.0",
		"lastModified": "2023-01-02",
	}

	newDoc, err := client.SaveInstance(ctx, "doc-inst-002", updatedType, newDocAttrs)
	if err != nil {
		log.Fatalf("Failed to create new document instance: %v", err)
	}

	fmt.Printf("Created new document instance with type version %d\n", newDoc.TypeVersion)
	for name, value := range newDoc.Attributes {
		fmt.Printf("  %s = %v\n", name, value)
	}

	// ---- STEP 6: Re-enable the attribute in a new type version ----
	fmt.Println("\n=== Step 6: Re-enabling the 'author' attribute in a new type version ===")

	// Re-enable the author attribute
	updatedType, err = client.SetAttributeDisabledState(ctx, docType.Name, authorAttr.Name, false)
	if err != nil {
		log.Fatalf("Failed to re-enable attribute: %v", err)
	}

	fmt.Printf("Re-enabled 'author' attribute in type version %d\n", updatedType.Version)

	// ---- STEP 7: Update existing instance with the re-enabled attribute ----
	fmt.Println("\n=== Step 7: Updating existing instance with the re-enabled attribute ===")

	updatedDoc, err = client.UpdateInstance(ctx, "doc-inst-002", map[string]interface{}{
		"author": "Jane Smith", // This should work now
	})

	if err != nil {
		log.Fatalf("Failed to update document: %v", err)
	}

	fmt.Printf("Updated document to type version %d\n", updatedDoc.TypeVersion)
	for name, value := range updatedDoc.Attributes {
		fmt.Printf("  %s = %v\n", name, value)
	}
}
