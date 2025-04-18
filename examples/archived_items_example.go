package main

import (
	"context"
	"fmt"
	"log"

	"github.com/zac300/flexitype/internal/adapters/repositories/memory"
	"github.com/zac300/flexitype/internal/application/services"
	"github.com/zac300/flexitype/internal/ports"
	"github.com/zac300/flexitype/pkg/sdk"
)

// This example demonstrates the soft delete (archive) functionality
func main() {
	// Create in-memory repositories
	typeRepo := memory.NewInMemoryTypeRepository()
	instanceRepo := memory.NewInMemoryInstanceRepository()

	// Create services
	typeService := services.NewTypeService(typeRepo, instanceRepo)
	instanceService := services.NewInstanceService(typeRepo, instanceRepo)

	// Create SDK client
	_ = sdk.NewClient(typeRepo, instanceRepo)

	ctx := context.Background()

	// Step 1: Create a type definition
	fmt.Println("=== Step 1: Creating a type definition ===")
	productType, err := typeService.SaveType(ctx, "Product", "A product type", "")
	if err != nil {
		log.Fatalf("Failed to create type: %v", err)
	}

	fmt.Printf("Created type: %s (v%d)\n", productType.Name, productType.Version)

	// Step 2: Add attributes to the type
	fmt.Println("\n=== Step 2: Adding attributes to the type ===")

	// Add name attribute
	nameAttr := sdk.NewAttribute("name", "Product name", sdk.StringType, true)
	nameAttr.AddValidationRule(&sdk.RequiredRule{})

	// Add price attribute
	priceAttr := sdk.NewAttribute("price", "Product price", sdk.FloatType, true)

	// Use the NewRangeRule helper function instead of struct literal
	minPrice := sdk.Float64Ptr(0.01)
	priceAttr.AddValidationRule(sdk.NewRangeRule(minPrice, nil))

	// Add attributes to the type
	productType, err = typeService.AddAttribute(ctx, productType.Name, nameAttr)
	if err != nil {
		log.Fatalf("Failed to add name attribute: %v", err)
	}

	productType, err = typeService.AddAttribute(ctx, productType.Name, priceAttr)
	if err != nil {
		log.Fatalf("Failed to add price attribute: %v", err)
	}

	fmt.Printf("Added attributes to type (now v%d):\n", productType.Version)
	for _, attr := range productType.Attributes {
		fmt.Printf("  - %s (%s)\n", attr.Name, attr.Name)
	}

	// Step 3: Create multiple instances of the type
	fmt.Println("\n=== Step 3: Creating instances ===")

	// Create a few products
	products := []struct {
		id    string
		name  string
		price float64
	}{
		{"prod-001", "Basic Widget", 9.99},
		{"prod-002", "Premium Widget", 19.99},
		{"prod-003", "Deluxe Widget", 29.99},
		{"prod-004", "Economy Widget", 4.99},
	}

	for _, p := range products {
		attrs := map[string]interface{}{
			"name":  p.name,
			"price": p.price,
		}

		instance, err := instanceService.SaveInstance(ctx, p.id, productType.Name, attrs)
		if err != nil {
			log.Fatalf("Failed to create product instance: %v", err)
		}

		fmt.Printf("Created product: %s (%s, $%.2f)\n",
			p.id, instance.Attributes["name"], instance.Attributes["price"])
	}

	// Step 4: List all active instances
	fmt.Println("\n=== Step 4: Listing all active instances ===")

	options := &ports.QueryOptions{
		TypeName:        productType.Name,
		IncludeArchived: false,
	}

	allInstances, count, err := instanceService.QueryInstancesWithOptions(ctx, options)
	if err != nil {
		log.Fatalf("Failed to query instances: %v", err)
	}

	fmt.Printf("Found %d active products\n", count)
	for _, instance := range allInstances {
		fmt.Printf("  - %s: %s, $%.2f\n",
			instance.ID,
			instance.Attributes["name"],
			instance.Attributes["price"])
	}

	// Step 5: Archive some instances
	fmt.Println("\n=== Step 5: Archiving instances ===")

	instancesToArchive := []string{"prod-001", "prod-003"}
	for _, id := range instancesToArchive {
		err := instanceService.ArchiveInstance(ctx, id)
		if err != nil {
			log.Fatalf("Failed to archive instance %s: %v", id, err)
		}
		fmt.Printf("Archived product: %s\n", id)
	}

	// Step 6: List active instances (excluding archived)
	fmt.Println("\n=== Step 6: Listing active instances (excluding archived) ===")

	activeInstances, activeCount, err := instanceService.QueryInstancesWithOptions(ctx, options)
	if err != nil {
		log.Fatalf("Failed to query active instances: %v", err)
	}

	fmt.Printf("Found %d active products\n", activeCount)
	for _, instance := range activeInstances {
		fmt.Printf("  - %s: %s, $%.2f\n",
			instance.ID,
			instance.Attributes["name"],
			instance.Attributes["price"])
	}

	// Step 7: List all instances (including archived)
	fmt.Println("\n=== Step 7: Listing all instances (including archived) ===")

	includeArchivedOptions := &ports.QueryOptions{
		TypeName:        productType.Name,
		IncludeArchived: true,
	}

	allWithArchived, totalCount, err := instanceService.QueryInstancesWithOptions(ctx, includeArchivedOptions)
	if err != nil {
		log.Fatalf("Failed to query all instances: %v", err)
	}

	fmt.Printf("Found %d total products (including archived)\n", totalCount)
	for _, instance := range allWithArchived {
		status := "Active"
		if instance.ArchivedAt != nil {
			status = fmt.Sprintf("Archived at %s", instance.ArchivedAt.Format("2006-01-02 15:04:05"))
		}

		fmt.Printf("  - %s: %s, $%.2f [%s]\n",
			instance.ID,
			instance.Attributes["name"],
			instance.Attributes["price"],
			status)
	}

	// Step 8: Unarchive an instance
	fmt.Println("\n=== Step 8: Unarchiving an instance ===")

	err = instanceService.UnarchiveInstance(ctx, "prod-001")
	if err != nil {
		log.Fatalf("Failed to unarchive instance: %v", err)
	}

	fmt.Printf("Unarchived product: prod-001\n")

	// Step 9: Final list of active instances
	fmt.Println("\n=== Step 9: Final list of active instances ===")

	finalActive, finalCount, err := instanceService.QueryInstancesWithOptions(ctx, options)
	if err != nil {
		log.Fatalf("Failed to query final active instances: %v", err)
	}

	fmt.Printf("Found %d active products\n", finalCount)
	for _, instance := range finalActive {
		fmt.Printf("  - %s: %s, $%.2f\n",
			instance.ID,
			instance.Attributes["name"],
			instance.Attributes["price"])
	}

	// Step 10: Demonstrate archiving a type
	fmt.Println("\n=== Step 10: Archiving a type ===")

	// Create a temporary type that we'll archive
	tempType, err := typeService.SaveType(ctx, "TemporaryType", "A type that will be archived", "")
	if err != nil {
		log.Fatalf("Failed to create temporary type: %v", err)
	}

	fmt.Printf("Created temporary type: %s (v%d)\n", tempType.Name, tempType.Version)

	// Archive the type
	err = typeService.ArchiveType(ctx, tempType.Name)
	if err != nil {
		log.Fatalf("Failed to archive type: %v", err)
	}

	fmt.Printf("Archived type: %s\n", tempType.Name)

	// List all types including archived
	allTypesOptions := &ports.QueryOptions{
		IncludeArchived: true,
	}

	allTypes, typeCount, err := typeRepo.List(ctx, allTypesOptions)
	if err != nil {
		log.Fatalf("Failed to list all types: %v", err)
	}

	fmt.Printf("Found %d total types (including archived)\n", typeCount)
	for _, t := range allTypes {
		status := "Active"
		if t.ArchivedAt != nil {
			status = fmt.Sprintf("Archived at %s", t.ArchivedAt.Format("2006-01-02 15:04:05"))
		}

		fmt.Printf("  - %s [%s]\n", t.Name, status)
	}

	// List only active types
	activeTypesOptions := &ports.QueryOptions{
		IncludeArchived: false,
	}

	activeTypes, activeTypeCount, err := typeRepo.List(ctx, activeTypesOptions)
	if err != nil {
		log.Fatalf("Failed to list active types: %v", err)
	}

	fmt.Printf("Found %d active types\n", activeTypeCount)
	for _, t := range activeTypes {
		fmt.Printf("  - %s\n", t.Name)
	}
}
