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

	// ---- STEP 1: Create a base type definition with multiple cascades of different weights ----
	fmt.Println("=== Step 1: Creating base type with multiple cascades ===")

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

	// Add signature required attribute with multiple cascades of different weights
	signatureAttr := sdk.NewAttribute("attr-004", "signatureRequired", "Requires manager signature", sdk.BooleanType, false)
	signatureAttr.SetDefaultValue(false)

	// First cascade: Basic amount threshold with low weight (150)
	signatureAttr.AddCascade("amount-threshold", true, core.CascadeInherit, "amount > 1000 => signatureRequired = true", 150)

	// Second cascade: High-risk vendor with medium weight (300)
	signatureAttr.AddCascade("high-risk-vendor", true, core.CascadeInherit, "vendor == \"HighRisk\" && amount > 500 => signatureRequired = true", 300)

	// Third cascade: International vendors with highest weight (500)
	signatureAttr.AddCascade("international-vendor", true, core.CascadeInherit, "vendor == \"International\" => signatureRequired = true", 500)

	// Add status attribute with cascade
	statusAttr := sdk.NewAttribute("attr-005", "status", "Purchase order status", sdk.StringType, true)
	statusAttr.SetDefaultValue("Draft")

	// Create a validation rule for allowed status values
	statusRule := sdk.NewEnumRule([]interface{}{"Draft", "Submitted", "Approved", "Rejected"})
	statusAttr.AddValidationRule(statusRule)

	// Add reason attribute that is conditionally required based on status
	reasonAttr := sdk.NewAttribute("attr-006", "rejectionReason", "Reason for rejection", sdk.StringType, false)
	reasonAttr.AddCascade("rejection-reason", true, core.CascadeInherit, "status == \"Rejected\" && isEmpty(rejectionReason) => rejectionReason = \"Please provide a reason\"", 200)

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
		if attr.HasEnabledCascades() {
			for _, cascade := range attr.GetCascades() {
				fmt.Printf("\n  [Cascade Weight=%d, Logic: %s]", cascade.Weight, cascade.Logic)
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

	// Override the signature attribute with different logic and weights
	specialSignatureAttr := sdk.NewAttribute("attr-004", "signatureRequired", "Requires manager signature", sdk.BooleanType, false)
	specialSignatureAttr.SetDefaultValue(false)

	// Add two cascades with different weights
	specialSignatureAttr.AddCascade("signature-for-5000-or-above", true, core.CascadeOverride, "amount > 5000 => signatureRequired = true", 200)
	specialSignatureAttr.AddCascade("vendor-signature-for-5000-or-above", true, core.CascadeOverride, "isNotEmpty(vendorApprovalRequired) && vendorApprovalRequired == true && amount > 2000 => signatureRequired = true", 400)

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
		if attr.HasEnabledCascades() {
			for _, cascade := range attr.GetCascades() {
				fmt.Printf("\n  [Cascade Weight=%d, Logic: %s]", cascade.Weight, cascade.Logic)
			}
		}
		fmt.Println()
	}

	// ---- STEP 3: Create instances to test cascade order/weight ----
	fmt.Println("\n=== Step 3: Creating instances to test cascade weights ===")

	// Test Case 1: Regular PO with international vendor
	// This should trigger the highest weight cascade (500) regardless of amount
	internationalPoAttrs := map[string]interface{}{
		"poNumber": "PO-123456",
		"vendor":   "International",
		"amount":   100.0, // Small amount
		"status":   "Approved",
	}

	internationalPo, err := client.SaveInstance(ctx, "po-inst-001", poType, internationalPoAttrs)
	if err != nil {
		log.Fatalf("Failed to create international purchase order: %v", err)
	}

	fmt.Println("Regular PO with International vendor (amount $100):")
	signatureRequired, _ := internationalPo.GetAttribute("signatureRequired")
	fmt.Printf("signatureRequired = %v (should be true due to highest weight cascade)\n", signatureRequired)

	// Test Case 2: Regular PO with HighRisk vendor and medium amount
	// This should trigger the medium weight cascade (300)
	highRiskPoAttrs := map[string]interface{}{
		"poNumber": "PO-789012",
		"vendor":   "HighRisk",
		"amount":   600.0, // Just above 500 threshold for high-risk vendors
		"status":   "Approved",
	}

	highRiskPo, err := client.SaveInstance(ctx, "po-inst-002", poType, highRiskPoAttrs)
	if err != nil {
		log.Fatalf("Failed to create high risk purchase order: %v", err)
	}

	fmt.Println("\nRegular PO with HighRisk vendor (amount $600):")
	signatureRequired, _ = highRiskPo.GetAttribute("signatureRequired")
	fmt.Printf("signatureRequired = %v (should be true due to 300-weighted cascade)\n", signatureRequired)

	// Test Case 3: Regular PO with amount just above the threshold
	// This should trigger the lowest weight cascade (150)
	largePoAttrs := map[string]interface{}{
		"poNumber": "PO-345678",
		"vendor":   "Regular Vendor",
		"amount":   1100.0, // Just above the 1000 threshold
		"status":   "Approved",
	}

	largePo, err := client.SaveInstance(ctx, "po-inst-003", poType, largePoAttrs)
	if err != nil {
		log.Fatalf("Failed to create large purchase order: %v", err)
	}

	fmt.Println("\nRegular PO with amount $1100:")
	signatureRequired, _ = largePo.GetAttribute("signatureRequired")
	fmt.Printf("signatureRequired = %v (should be true due to 150-weighted cascade)\n", signatureRequired)

	// Test Case 4: Special PO with vendor approval and amount above threshold
	// This should trigger the high weight cascade (400) from the special type
	specialPoAttrs := map[string]interface{}{
		"poNumber":               "PO-567890",
		"vendor":                 "Special Vendor",
		"amount":                 3000.0, // Between 2000 and 5000
		"status":                 "Approved",
		"vendorApprovalRequired": true,
	}

	specialPo, err := client.SaveInstance(ctx, "po-inst-004", specialPoType, specialPoAttrs)
	if err != nil {
		log.Fatalf("Failed to create special purchase order: %v", err)
	}

	fmt.Println("\nSpecial PO with vendor approval and amount $3000:")
	signatureRequired, _ = specialPo.GetAttribute("signatureRequired")
	fmt.Printf("signatureRequired = %v (should be true due to 400-weighted cascade)\n", signatureRequired)

	// Test Case 5: Small amount HighRisk special PO with vendor approval
	// This should NOT trigger any cascades in the special type (values too low)
	smallHighRiskSpecialPoAttrs := map[string]interface{}{
		"poNumber":               "PO-111222",
		"vendor":                 "HighRisk",
		"amount":                 450.0, // Below all thresholds in special type
		"status":                 "Approved",
		"vendorApprovalRequired": true,
	}

	// Note that in the special type, the HighRisk cascade is overridden and no longer applies
	smallHighRiskSpecialPo, err := client.SaveInstance(ctx, "po-inst-005", specialPoType, smallHighRiskSpecialPoAttrs)
	if err != nil {
		log.Fatalf("Failed to create small high risk special purchase order: %v", err)
	}

	fmt.Println("\nSpecial PO with HighRisk vendor and amount $450:")
	signatureRequired, _ = smallHighRiskSpecialPo.GetAttribute("signatureRequired")
	fmt.Printf("signatureRequired = %v (should be false as it doesn't meet any thresholds in the special type)\n", signatureRequired)
}
