package v1

import (
	"context"
	"testing"

	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	policy "github.com/ERONIS/wb-service/internal/core/transport/wb/policy"
)

type executorCall struct {
	operation policy.Operation
	query     any
	body      any
	target    any
}

type recordingExecutor struct {
	calls []executorCall
}

func (executor *recordingExecutor) Execute(
	_ context.Context,
	operation policy.Operation,
	query any,
	body any,
	target any,
) error {
	executor.calls = append(executor.calls, executorCall{
		operation: operation,
		query:     query,
		body:      body,
		target:    target,
	})

	return nil
}

func TestContentV1MethodsSelectCatalogOperations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		operation string
		kind      policy.OperationKind
		hasQuery  bool
		hasBody   bool
		invoke    func(context.Context, ContentV1Interface) error
	}{
		{
			name:      "parent categories",
			operation: "content.object.parent-all",
			kind:      policy.OperationKindRead,
			hasQuery:  true,
			invoke: func(ctx context.Context, content ContentV1Interface) error {
				_, err := content.Categories().ParentCategories(
					ctx,
					contentapi.ParentCategoriesQuery{},
				)
				return err
			},
		},
		{
			name:      "subjects",
			operation: "content.object.all",
			kind:      policy.OperationKindRead,
			hasQuery:  true,
			invoke: func(ctx context.Context, content ContentV1Interface) error {
				_, err := content.Categories().Subjects(
					ctx,
					contentapi.SubjectsQuery{},
				)
				return err
			},
		},
		{
			name:      "subject characteristics",
			operation: "content.object.characteristics",
			kind:      policy.OperationKindRead,
			hasQuery:  true,
			invoke: func(ctx context.Context, content ContentV1Interface) error {
				_, err := content.Categories().SubjectCharacteristics(
					ctx,
					1,
					contentapi.SubjectCharacteristicsQuery{},
				)
				return err
			},
		},
		{
			name:      "brands",
			operation: "content.brands",
			kind:      policy.OperationKindRead,
			hasQuery:  true,
			invoke: func(ctx context.Context, content ContentV1Interface) error {
				_, err := content.Categories().Brands(
					ctx,
					contentapi.BrandsQuery{},
				)
				return err
			},
		},
		{
			name:      "directory colors",
			operation: "content.directory.colors",
			kind:      policy.OperationKindRead,
			hasQuery:  true,
			invoke: func(ctx context.Context, content ContentV1Interface) error {
				_, err := content.Directories().Colors(ctx, contentapi.DirectoryQuery{})
				return err
			},
		},
		{
			name:      "directory kinds",
			operation: "content.directory.kinds",
			kind:      policy.OperationKindRead,
			hasQuery:  true,
			invoke: func(ctx context.Context, content ContentV1Interface) error {
				_, err := content.Directories().Kinds(ctx, contentapi.DirectoryQuery{})
				return err
			},
		},
		{
			name:      "directory countries",
			operation: "content.directory.countries",
			kind:      policy.OperationKindRead,
			hasQuery:  true,
			invoke: func(ctx context.Context, content ContentV1Interface) error {
				_, err := content.Directories().Countries(ctx, contentapi.DirectoryQuery{})
				return err
			},
		},
		{
			name:      "directory seasons",
			operation: "content.directory.seasons",
			kind:      policy.OperationKindRead,
			hasQuery:  true,
			invoke: func(ctx context.Context, content ContentV1Interface) error {
				_, err := content.Directories().Seasons(ctx, contentapi.DirectoryQuery{})
				return err
			},
		},
		{
			name:      "directory VAT",
			operation: "content.directory.vat",
			kind:      policy.OperationKindRead,
			hasQuery:  true,
			invoke: func(ctx context.Context, content ContentV1Interface) error {
				_, err := content.Directories().VAT(ctx, contentapi.DirectoryQuery{})
				return err
			},
		},
		{
			name:      "directory TNVED",
			operation: "content.directory.tnved",
			kind:      policy.OperationKindRead,
			hasQuery:  true,
			invoke: func(ctx context.Context, content ContentV1Interface) error {
				_, err := content.Directories().TNVED(ctx, contentapi.DirectoryTNVEDQuery{})
				return err
			},
		},
		{
			name:      "card limits",
			operation: "content.cards.limits",
			kind:      policy.OperationKindRead,
			invoke: func(ctx context.Context, content ContentV1Interface) error {
				_, err := content.Cards().Limits(ctx)
				return err
			},
		},
		{
			name:      "card list",
			operation: "content.cards.list",
			kind:      policy.OperationKindRead,
			hasBody:   true,
			invoke: func(ctx context.Context, content ContentV1Interface) error {
				_, err := content.Cards().List(ctx, contentapi.CardsListRequest{})
				return err
			},
		},
		{
			name:      "trash card list",
			operation: "content.cards.trash-list",
			kind:      policy.OperationKindRead,
			hasBody:   true,
			invoke: func(ctx context.Context, content ContentV1Interface) error {
				_, err := content.Cards().TrashList(ctx, contentapi.TrashCardsListRequest{})
				return err
			},
		},
		{
			name:      "card error list",
			operation: "content.cards.error-list",
			kind:      policy.OperationKindRead,
			hasBody:   true,
			invoke: func(ctx context.Context, content ContentV1Interface) error {
				_, err := content.Cards().ErrorList(ctx, contentapi.CardsErrorListRequest{})
				return err
			},
		},
		{
			name:      "upload cards",
			operation: "content.cards.upload",
			kind:      policy.OperationKindMutation,
			hasBody:   true,
			invoke: func(ctx context.Context, content ContentV1Interface) error {
				_, err := content.Cards().Upload(ctx, contentapi.UploadCardsRequest{})
				return err
			},
		},
		{
			name:      "upload cards add",
			operation: "content.cards.upload-add",
			kind:      policy.OperationKindMutation,
			hasBody:   true,
			invoke: func(ctx context.Context, content ContentV1Interface) error {
				_, err := content.Cards().UploadAdd(ctx, contentapi.UploadCardsAddRequest{})
				return err
			},
		},
		{
			name:      "save media",
			operation: "content.media.save",
			kind:      policy.OperationKindMutation,
			hasBody:   true,
			invoke: func(ctx context.Context, content ContentV1Interface) error {
				_, err := content.Media().SaveByLinks(ctx, contentapi.SaveMediaByLinksRequest{})
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			executor := &recordingExecutor{}
			content, err := NewContentV1Client(executor)
			if err != nil {
				t.Fatalf("create Content client: %v", err)
			}
			if err := test.invoke(context.Background(), content); err != nil {
				t.Fatalf("invoke typed method: %v", err)
			}
			if len(executor.calls) != 1 {
				t.Fatalf("executor calls = %d, want 1", len(executor.calls))
			}

			call := executor.calls[0]
			if got := call.operation.ID().String(); got != test.operation {
				t.Fatalf("operation = %q, want %q", got, test.operation)
			}
			if call.operation.Kind() != test.kind {
				t.Fatalf("operation kind = %q, want %q", call.operation.Kind(), test.kind)
			}
			if (call.query != nil) != test.hasQuery {
				t.Fatalf("query presence = %v, want %v", call.query != nil, test.hasQuery)
			}
			if (call.body != nil) != test.hasBody {
				t.Fatalf("body presence = %v, want %v", call.body != nil, test.hasBody)
			}
			if call.target == nil {
				t.Fatal("response target is nil")
			}
		})
	}
}

func TestSubjectCharacteristicsRejectsInvalidIDBeforeExecute(t *testing.T) {
	t.Parallel()

	executor := &recordingExecutor{}
	content, err := NewContentV1Client(executor)
	if err != nil {
		t.Fatalf("create Content client: %v", err)
	}

	_, err = content.Categories().SubjectCharacteristics(
		context.Background(),
		0,
		contentapi.SubjectCharacteristicsQuery{},
	)
	if err == nil {
		t.Fatal("invalid subject ID unexpectedly succeeded")
	}
	if len(executor.calls) != 0 {
		t.Fatalf("executor calls = %d, want 0", len(executor.calls))
	}
}
