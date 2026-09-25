package preparation

import (
	"bytes"
	"context"
	"testing"

	"github.com/ERONIS/wb-service/internal/core/domain/cardpipeline"
	"github.com/ERONIS/wb-service/internal/core/observability"
	contentapi "github.com/ERONIS/wb-service/internal/core/transport/wb/api/content/v1"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type diagnosticTransport struct {
	CatalogTransport
	characteristics []contentapi.SubjectCharacteristic
}

func (transport diagnosticTransport) CardsLimits(context.Context, CabinetID) (contentapi.CardsLimitsResponse, error) {
	return contentapi.CardsLimitsResponse{Data: contentapi.CardsLimits{FreeLimits: 10}}, nil
}

func (transport diagnosticTransport) Subjects(context.Context, CabinetID, contentapi.SubjectsQuery) (contentapi.SubjectsResponse, error) {
	return contentapi.SubjectsResponse{Data: []contentapi.Subject{{SubjectID: 1, SubjectName: "Футболки"}}}, nil
}

func (transport diagnosticTransport) SubjectCharacteristics(context.Context, CabinetID, int64, contentapi.SubjectCharacteristicsQuery) (contentapi.SubjectCharacteristicsResponse, error) {
	return contentapi.SubjectCharacteristicsResponse{Data: transport.characteristics}, nil
}

func (transport diagnosticTransport) VAT(context.Context, CabinetID, contentapi.DirectoryQuery) (contentapi.DirectoryVATResponse, error) {
	return contentapi.DirectoryVATResponse{Data: []string{"20%"}}, nil
}

func TestPrepareTargetSkipsUnknownCharacteristic(t *testing.T) {
	for _, source := range []string{"variant.characteristics", "vat_rate"} {
		t.Run(source, func(t *testing.T) {
			group := SourceGroup{Items: []SourceItem{{
				Position: 7,
				Card: cardpipeline.Card{
					SourceRows: []int{8, 9}, Category: "Футболки",
					Variant: cardpipeline.Variant{VendorCode: "vendor-7"},
				},
			}}}
			if source == "vat_rate" {
				group.Items[0].Card.VATRate = "20%"
			} else {
				group.Items[0].Card.Variant.Characteristics = []cardpipeline.RawCharacteristic{
					{ID: 999, Name: "Неизвестная характеристика", Value: []string{"значение", "другое"}},
				}
			}
			transport := diagnosticTransport{characteristics: []contentapi.SubjectCharacteristic{{
				CharacteristicID: 10, SubjectID: 1, Name: "Материал", MaxCount: 1, CharacteristicType: 1,
			}}}
			var output bytes.Buffer
			logger := zap.New(zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.AddSync(&output), zap.WarnLevel))
			ctx := observability.WithCorrelation(context.Background(), observability.Correlation{BatchID: 2, TransferID: 3, GroupTargetID: 4})
			result, err := NewPreparer(transport, logger).PrepareTarget(ctx, "shop_001", group)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Outcome.Code == OutcomeCharacteristicInvalid && result.Outcome.Field == "characteristic_unknown" {
				t.Fatalf("expected unknown characteristic to be skipped, but got rejection: %+v", result)
			}
		})
	}
}

