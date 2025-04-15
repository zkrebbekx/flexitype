package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/bufbuild/connect-go"

	flexitypev1 "github.com/zac300/flexitype/api/flexitypev1"
	flexitypev1connect "github.com/zac300/flexitype/api/flexitypev1connect"
)

func main() {
	serverAddr := flag.String("server", "http://localhost:8080", "FlexiType server address")
	action := flag.String("action", "list", "Action to perform (create, get, list, disable-attr, enable-attr)")
	typeID := flag.String("id", "", "Type ID for get/create actions")
	name := flag.String("name", "", "Type name for create action")
	description := flag.String("desc", "", "Type description for create action")
	parentID := flag.String("parent", "", "Parent type ID for create action")
	attrID := flag.String("attr", "", "Attribute ID for attribute-related actions")

	flag.Parse()

	// Create a Connect client
	client := flexitypev1connect.NewFlexiTypeServiceClient(
		http.DefaultClient,
		*serverAddr,
	)

	ctx := context.Background()

	switch *action {
	case "create":
		if *typeID == "" || *name == "" {
			log.Fatal("Type ID and name are required for create action")
		}

		// Create a new type
		req := &flexitypev1.CreateTypeRequest{
			Id:           *typeID,
			Name:         *name,
			Description:  *description,
			ParentTypeId: *parentID,
		}

		res, err := client.CreateType(ctx, connect.NewRequest(req))
		if err != nil {
			log.Fatalf("Failed to create type: %v", err)
		}

		fmt.Printf("Created type: %s (version %d)\n",
			res.Msg.Type.Name,
			res.Msg.Type.Version)

	case "get":
		if *typeID == "" {
			log.Fatal("Type ID is required for get action")
		}

		// Get a type by ID
		req := &flexitypev1.GetTypeRequest{
			Id: *typeID,
		}

		res, err := client.GetType(ctx, connect.NewRequest(req))
		if err != nil {
			log.Fatalf("Failed to get type: %v", err)
		}

		typeDef := res.Msg.Type
		fmt.Printf("Type: %s (ID: %s, Version: %d)\n",
			typeDef.Name,
			typeDef.Id,
			typeDef.Version)
		fmt.Printf("Description: %s\n", typeDef.Description)

		if typeDef.ParentTypeId != "" {
			fmt.Printf("Parent type: %s\n", typeDef.ParentTypeId)
		}

		fmt.Printf("Attributes: %d\n", len(typeDef.Attributes))
		for i, attr := range typeDef.Attributes {
			fmt.Printf("  %d. %s (%s)", i+1, attr.Name, attr.DataType)
			if attr.Disabled {
				fmt.Printf(" [DISABLED]")
			}
			fmt.Println()

			// Display all cascades
			if len(attr.Cascades) > 0 {
				fmt.Printf("     Cascades: %d\n", len(attr.Cascades))
				for t, cascade := range attr.Cascades {
					fmt.Printf("       %d. %s [%s]\n", t+1, cascade.Behavior, cascade.Logic)
				}
			}
		}

	case "list":
		// List all types
		req := &flexitypev1.ListTypesRequest{}

		res, err := client.ListTypes(ctx, connect.NewRequest(req))
		if err != nil {
			log.Fatalf("Failed to list types: %v", err)
		}

		fmt.Printf("Found %d types:\n", len(res.Msg.Types))
		for i, typeDef := range res.Msg.Types {
			fmt.Printf("%d. %s (ID: %s, Version: %d)\n",
				i+1,
				typeDef.Name,
				typeDef.Id,
				typeDef.Version)
		}

	case "disable-attr":
		if *typeID == "" || *attrID == "" {
			log.Fatal("Type ID and attribute ID are required for disable-attr action")
		}

		// Disable an attribute
		req := &flexitypev1.SetAttributeDisabledStateRequest{
			TypeId:      *typeID,
			AttributeId: *attrID,
			Disabled:    true,
		}

		res, err := client.SetAttributeDisabledState(ctx, connect.NewRequest(req))
		if err != nil {
			log.Fatalf("Failed to disable attribute: %v", err)
		}

		fmt.Printf("Disabled attribute %s in type %s (version now %d)\n",
			*attrID,
			res.Msg.Type.Name,
			res.Msg.Type.Version)

	case "enable-attr":
		if *typeID == "" || *attrID == "" {
			log.Fatal("Type ID and attribute ID are required for enable-attr action")
		}

		// Enable an attribute
		req := &flexitypev1.SetAttributeDisabledStateRequest{
			TypeId:      *typeID,
			AttributeId: *attrID,
			Disabled:    false,
		}

		res, err := client.SetAttributeDisabledState(ctx, connect.NewRequest(req))
		if err != nil {
			log.Fatalf("Failed to enable attribute: %v", err)
		}

		fmt.Printf("Enabled attribute %s in type %s (version now %d)\n",
			*attrID,
			res.Msg.Type.Name,
			res.Msg.Type.Version)

	default:
		fmt.Fprintf(os.Stderr, "Unknown action: %s\n", *action)
		flag.Usage()
		os.Exit(1)
	}
}
