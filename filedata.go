package lds3

import (
	"bytes"
	"encoding/json"
	"fmt"
	"unicode"

	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"
	"github.com/launchdarkly/go-server-sdk-evaluation/v3/ldmodel"
	"github.com/launchdarkly/go-server-sdk/v7/subsystems/ldstoretypes"

	"gopkg.in/ghodss/yaml.v1"
)

// document is the parsed form of a single data object. A document may contain full flag
// definitions and segment definitions. This is the format used by the SDK's file data sources,
// except that their simplified flag-key-to-value entries ("flagValues") are not supported. That
// section is still parsed, only so that its presence can be rejected with a clear error.
type document struct {
	Flags      *map[string]ldmodel.FeatureFlag
	FlagValues *map[string]ldvalue.Value
	Segments   *map[string]ldmodel.Segment
}

func parseDocument(rawData []byte) (document, error) {
	var data document
	var err error
	if detectJSON(rawData) {
		err = json.Unmarshal(rawData, &data)
	} else {
		err = yaml.Unmarshal(rawData, &data)
	}
	if err != nil {
		err = fmt.Errorf("error parsing data: %s", err)
	}
	return data, err
}

func detectJSON(rawData []byte) bool {
	// A valid JSON document for our purposes must be an object, i.e. it must start with '{'.
	// TrimLeftFunc returns a subslice, so no copy of the data is made.
	return bytes.HasPrefix(bytes.TrimLeftFunc(rawData, unicode.IsSpace), []byte("{"))
}

// documentItemsResult holds a document's items as store descriptors. Item values are
// *ldmodel.FeatureFlag or *ldmodel.Segment. The relative order of items is unspecified;
// consumers key items by their Key.
type documentItemsResult struct {
	Flags    []ldstoretypes.KeyedItemDescriptor
	Segments []ldstoretypes.KeyedItemDescriptor
}

// documentItems converts a parsed document's definitions into store descriptors.
func documentItems(doc document) documentItemsResult {
	var result documentItemsResult
	if doc.Flags != nil {
		for key, f := range *doc.Flags {
			result.Flags = append(result.Flags, ldstoretypes.KeyedItemDescriptor{
				Key:  key,
				Item: ldstoretypes.ItemDescriptor{Version: f.Version, Item: &f},
			})
		}
	}
	if doc.Segments != nil {
		for key, s := range *doc.Segments {
			result.Segments = append(result.Segments, ldstoretypes.KeyedItemDescriptor{
				Key:  key,
				Item: ldstoretypes.ItemDescriptor{Version: s.Version, Item: &s},
			})
		}
	}
	return result
}
