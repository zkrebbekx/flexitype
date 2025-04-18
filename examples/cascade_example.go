package main

import (
	"context"
	"fmt"
	"log"

	"github.com/zac300/flexitype/internal/adapters/repositories/memory"
	"github.com/zac300/flexitype/internal/domain/core"
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

	// ---- STEP 1: Create a base type definition with cascades ----
	fmt.Println("=== Step 1: Creating base type with cascades ===")

	// Create a purchase order type
	poType, err := client.SaveType(ctx, "po-001", "PurchaseOrder", "Purchase order record")
	if err != nil {
		log.Fatalf("Failed to create purchase order type: %v", err)
	}

	// Add basic attributes
	poNumberAttr := sdk.NewAttribute("attr-001", "poNumber", "Purchase order number", sdk.StringType, true)
	poNumberAttr.AddValidationRule(&sdk.RequiredRule{})

	patternRule, err := sdk.NewPatternRule("PO-[0-9]{6}")
	if err != nil {
		log.Fatalf("Failed to create pattern rule: %v", err)
	}
	poNumberAttr.AddValidationRule(patternRule)

	vendorAttr := sdk.NewAttribute("attr-002", "vendor", "Vendor name", sdk.StringType, true)
	vendorAttr.AddValidationRule(&sdk.RequiredRule{})

	amountAttr := sdk.NewAttribute("attr-003", "amount", "Purchase order amount", sdk.FloatType, true)
	amountAttr.AddValidationRule(&sdk.RequiredRule{})

	minValue := sdk.Float64Ptr(0.01)
	rangeRule := sdk.NewRangeRule(minValue, nil)
	amountAttr.AddValidationRule(rangeRule)

	// Add signature required attribute as a cascade with logic
	signatureAttr := sdk.NewAttribute("attr-004", "signatureRequired", "Requires manager signature", sdk.BooleanType, false)
	signatureAttr.SetDefaultValue(false)

	// Set this as a cascade that will be inherited by child types
	// The logic uses complex expression: if amount > 1000 OR (vendor == "HighRisk" AND amount > 500) => signatureRequired = true
	signatureAttr.AddCascade("high-risk", true, core.CascadeInherit, "(amount > 1000) || (vendor == \"HighRisk\" && amount > 500) => signatureRequired = true", 100)

	// Add status attribute (for demonstrating linked dropdowns)
	statusAttr := sdk.NewAttribute("attr-005", "status", "Purchase order status", sdk.StringType, true)
	statusAttr.SetDefaultValue("Draft")

	// Create a validation rule for allowed status values
	statusRule := sdk.NewEnumRule([]interface{}{"Draft", "Submitted", "Approved", "Rejected"})
	statusAttr.AddValidationRule(statusRule)

	// Add reason attribute that is conditionally required based on status
	reasonAttr := sdk.NewAttribute("attr-006", "rejectionReason", "Reason for rejection", sdk.StringType, false)
	reasonAttr.AddCascade("rejected-reason", true, core.CascadeInherit, "status == \"Rejected\" && isEmpty(rejectionReason) => rejectionReason = \"Please provide a reason\"", 200)

	// Add all attributes to the type
	client.AddAttribute(ctx, poType.ID, poNumberAttr)
	client.AddAttribute(ctx, poType.ID, vendorAttr)
	client.AddAttribute(ctx, poType.ID, amountAttr)
	client.AddAttribute(ctx, poType.ID, signatureAttr)
	client.AddAttribute(ctx, poType.ID, statusAttr)
	client.AddAttribute(ctx, poType.ID, reasonAttr)

	// Save the type
	err = typeRepo.Save(ctx, poType)
	if err != nil {
		log.Fatalf("Failed to save purchase order type: %v", err)
	}

	fmt.Printf("Created PurchaseOrder type with %d attributes\n", len(poType.Attributes))
	for _, attr := range poType.Attributes {
		fmt.Printf("- %s (%s)", attr.Name, attr.DataType)
		for _, t := range attr.Cascades {
			if t.Enabled {
				fmt.Printf(" [Cascade: %s]", t.Logic)
			}
		}
		fmt.Println()
	}

	// ---- STEP 2: Create a child type with overridden cascades ----
	fmt.Println("\n=== Step 2: Creating child type with overridden cascades ===")

	// Create a special purchase order type with different approval rules
	specialPoType, err := client.SaveType(ctx, "po-002", "SpecialPurchaseOrder", "Special purchase order with different rules")
	if err != nil {
		log.Fatalf("Failed to create special purchase order type: %v", err)
	}

	// Set parent type
	specialPoType.SetParentType(poType)

	// Override the signature attribute with different logic
	specialSignatureAttr := sdk.NewAttribute("attr-004", "signatureRequired", "Requires manager signature", sdk.BooleanType, false)
	specialSignatureAttr.SetDefaultValue(false)

	// Set this as a cascade with different threshold (5000 instead of 1000)
	specialSignatureAttr.AddCascade("signature-required", true, core.CascadeOverride, "amount > 5000 || (isNotEmpty(vendorApprovalRequired) && vendorApprovalRequired == true && amount > 2000) => signatureRequired = true", 200)

	// Add vendor approval attribute (unique to this type)
	vendorApprovalAttr := sdk.NewAttribute("attr-007", "vendorApprovalRequired", "Requires vendor pre-approval", sdk.BooleanType, true)
	vendorApprovalAttr.SetDefaultValue(false)

	// Add to type
	client.AddAttribute(ctx, specialPoType.ID, specialSignatureAttr)
	client.AddAttribute(ctx, specialPoType.ID, vendorApprovalAttr)

	// Save the type
	err = typeRepo.Save(ctx, specialPoType)
	if err != nil {
		log.Fatalf("Failed to save special purchase order type: %v", err)
	}

	fmt.Printf("Created SpecialPurchaseOrder type inheriting from PurchaseOrder\n")
	fmt.Printf("All attributes (including inherited): %d\n", len(specialPoType.GetAllAttributes()))

	for _, attr := range specialPoType.GetAllAttributes() {
		fmt.Printf("- %s (%s)", attr.Name, attr.DataType)
		for _, t := range attr.Cascades {
			if t.Enabled {
				fmt.Printf(" [Cascade: %s]", t.Logic)
			}
		}
		fmt.Println()
	}

	// ---- STEP 3: Create an instance and see cascade logic in action ----
	fmt.Println("\n=== Step 3: Creating instances with cascade logic ===")

	// Create a regular purchase order with amount under threshold
	smallPoAttrs := map[string]interface{}{
		"poNumber": "PO-123456",
		"vendor":   "Acme Corp",
		"amount":   500.0,
		"status":   "Approved",
	}

	smallPo, err := client.SaveInstance(ctx, "po-inst-001", poType, smallPoAttrs)
	if err != nil {
		log.Fatalf("Failed to create small purchase order: %v", err)
	}

	fmt.Println("Regular PO with amount $500:")
	signatureRequired, _ := smallPo.GetAttribute("signatureRequired")
	fmt.Printf("signatureRequired = %v (should be false since amount < 1000)\n", signatureRequired)

	// Create a regular purchase order with amount over threshold
	largePoAttrs := map[string]interface{}{
		"poNumber": "PO-789012",
		"vendor":   "Globex Inc",
		"amount":   2500.0,
		"status":   "Approved",
	}

	largePo, err := client.SaveInstance(ctx, "po-inst-002", poType, largePoAttrs)
	if err != nil {
		log.Fatalf("Failed to create large purchase order: %v", err)
	}

	fmt.Println("\nRegular PO with amount $2500:")
	signatureRequired, _ = largePo.GetAttribute("signatureRequired")
	fmt.Printf("signatureRequired = %v (should be true since amount > 1000)\n", signatureRequired)

	// Create a regular purchase order with high risk vendor but small amount
	highRiskPoAttrs := map[string]interface{}{
		"poNumber": "PO-555555",
		"vendor":   "HighRisk",
		"amount":   750.0,
		"status":   "Approved",
	}

	highRiskPo, err := client.SaveInstance(ctx, "po-inst-002b", poType, highRiskPoAttrs)
	if err != nil {
		log.Fatalf("Failed to create high risk purchase order: %v", err)
	}

	fmt.Println("\nRegular PO with high risk vendor and amount $750:")
	signatureRequired, _ = highRiskPo.GetAttribute("signatureRequired")
	fmt.Printf("signatureRequired = %v (should be true since vendor is HighRisk and amount > 500)\n", signatureRequired)

	// Create a special purchase order with amount that would trigger regular PO but not special PO
	specialPoAttrs := map[string]interface{}{
		"poNumber":               "PO-345678",
		"vendor":                 "Special Vendor",
		"amount":                 3000.0,
		"status":                 "Approved",
		"vendorApprovalRequired": true,
	}

	specialPo, err := client.SaveInstance(ctx, "po-inst-003", specialPoType, specialPoAttrs)
	if err != nil {
		log.Fatalf("Failed to create special purchase order: %v", err)
	}

	fmt.Println("\nSpecial PO with amount $3000:")
	signatureRequired, _ = specialPo.GetAttribute("signatureRequired")
	fmt.Printf("signatureRequired = %v (should be false since amount < 5000 for special PO)\n", signatureRequired)

	// Create a special purchase order with amount that would trigger even the special PO threshold
	veryLargeSpecialPoAttrs := map[string]interface{}{
		"poNumber":               "PO-999999",
		"vendor":                 "Major Corp",
		"amount":                 7500.0,
		"status":                 "Approved",
		"vendorApprovalRequired": true,
	}

	veryLargeSpecialPo, err := client.SaveInstance(ctx, "po-inst-004", specialPoType, veryLargeSpecialPoAttrs)
	if err != nil {
		log.Fatalf("Failed to create very large special purchase order: %v", err)
	}

	fmt.Println("\nSpecial PO with amount $7500:")
	signatureRequired, _ = veryLargeSpecialPo.GetAttribute("signatureRequired")
	fmt.Printf("signatureRequired = %v (should be true since amount > 5000)\n", signatureRequired)

	// ---- STEP 4: Demonstrate linked dropdown behavior ----
	fmt.Println("\n=== Step 4: Demonstrating linked/dependent fields ===")

	// Create a rejected PO that should require a rejection reason
	rejectedPoAttrs := map[string]interface{}{
		"poNumber":        "PO-654321",
		"vendor":          "Rejected Vendor",
		"amount":          1000.0,
		"status":          "Rejected",
		"rejectionReason": "Price too high",
	}

	rejectedPo, err := client.SaveInstance(ctx, "po-inst-005", poType, rejectedPoAttrs)
	if err != nil {
		log.Fatalf("Failed to create rejected purchase order: %v", err)
	}

	fmt.Println("Rejected PO:")
	status, _ := rejectedPo.GetAttribute("status")
	fmt.Printf("status = %v\n", status)
	reason, _ := rejectedPo.GetAttribute("rejectionReason")
	fmt.Printf("rejectionReason = %v (required because status is Rejected)\n", reason)

	// Try updating the status to Approved (should not require rejection reason)
	_, err = client.UpdateInstance(ctx, "po-inst-005", map[string]interface{}{
		"status": "Approved",
	})
	if err != nil {
		log.Fatalf("Failed to update rejected PO: %v", err)
	}

	// Get the updated instance
	updatedPo, err := client.GetInstance(ctx, "po-inst-005")
	if err != nil {
		log.Fatalf("Failed to get updated PO: %v", err)
	}

	fmt.Println("\nAfter updating status to Approved:")
	status, _ = updatedPo.GetAttribute("status")
	fmt.Printf("status = %v\n", status)
	reason, err = updatedPo.GetAttribute("rejectionReason")
	if err != nil {
		fmt.Printf("rejectionReason is not set (as expected)\n")
	} else {
		fmt.Printf("rejectionReason = %v\n", reason)
	}
}
