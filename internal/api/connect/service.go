package service

import (
	"github.com/zkrebbekx/flexitype/internal/domain/service"
)

// Service implements the Connect service interface
type Service struct {
	typeDefinitionService           *service.TypeDefinitionService
	attributeService                *service.AttributeService
	attributeValueService           *service.AttributeValueService
	attributeValueDependencyService *service.AttributeValueDependencyService
}

// NewService creates a new Connect service
func NewService(
	typeDefinitionService *service.TypeDefinitionService,
	attributeService *service.AttributeService,
	attributeValueService *service.AttributeValueService,
	attributeValueDependencyService *service.AttributeValueDependencyService,
) *Service {
	return &Service{
		typeDefinitionService:           typeDefinitionService,
		attributeService:                attributeService,
		attributeValueService:           attributeValueService,
		attributeValueDependencyService: attributeValueDependencyService,
	}
} 