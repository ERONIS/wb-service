package cardimport_service

import "github.com/ERONIS/wb-service/internal/feature/cardimport/service/aggregation"

func AggregateParsedFile(parsed ParsedFile) AggregatedFile {
	return aggregation.AggregateParsedFile(parsed)
}
