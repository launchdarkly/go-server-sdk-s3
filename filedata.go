package lds3

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"
	"github.com/launchdarkly/go-server-sdk-evaluation/v3/ldbuilders"
	"github.com/launchdarkly/go-server-sdk-evaluation/v3/ldmodel"
	"github.com/launchdarkly/go-server-sdk/v7/subsystems/ldstoretypes"

	"gopkg.in/ghodss/yaml.v1"
)

// document is the parsed form of a single data object. A document may contain full flag
// definitions, simplified flag-key-to-value entries, and segment definitions. This is the same
// format used by the SDK's file data sources.
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
	// A valid JSON document for our purposes must be an object, i.e. it must start with '{'
	return strings.HasPrefix(strings.TrimLeftFunc(string(rawData), unicode.IsSpace), "{")
}

// mergeResult holds the merged items from one or more documents. Item values are
// *ldmodel.FeatureFlag or *ldmodel.Segment.
//
// Ordering is deterministic at document granularity only: all of one document's items precede the
// next document's, matching the order the documents were given, but the relative order of items
// within a single document is unspecified. Consumers key items by their Key and must not rely on
// within-document ordering.
type mergeResult struct {
	Flags    []ldstoretypes.KeyedItemDescriptor
	Segments []ldstoretypes.KeyedItemDescriptor
}

type itemCategory string

const (
	flagCategory    itemCategory = "flag"
	segmentCategory itemCategory = "segment"
)

// merge combines the items of the given documents, expanding flag-value entries into full flag
// definitions and applying the given duplicate-key handling. An unrecognized
// DuplicateKeysHandling value behaves as DuplicateKeysFail.
func merge(duplicateKeysHandling DuplicateKeysHandling, docs ...document) (mergeResult, error) {
	var result mergeResult
	seenKeys := map[itemCategory]map[string]bool{
		flagCategory:    {},
		segmentCategory: {},
	}

	insert := func(
		items *[]ldstoretypes.KeyedItemDescriptor,
		category itemCategory,
		key string,
		data ldstoretypes.ItemDescriptor,
	) error {
		if seenKeys[category][key] {
			switch duplicateKeysHandling {
			case DuplicateKeysIgnoreAllButFirst:
				return nil
			default:
				return fmt.Errorf("%s '%s' is specified by multiple objects", category, key)
			}
		}
		*items = append(*items, ldstoretypes.KeyedItemDescriptor{Key: key, Item: data})
		seenKeys[category][key] = true
		return nil
	}

	for _, d := range docs {
		if d.Flags != nil {
			for key, f := range *d.Flags {
				data := ldstoretypes.ItemDescriptor{Version: f.Version, Item: &f}
				if err := insert(&result.Flags, flagCategory, key, data); err != nil {
					return mergeResult{}, err
				}
			}
		}
		if d.FlagValues != nil {
			for key, value := range *d.FlagValues {
				flag := makeFlagWithValue(key, value)
				data := ldstoretypes.ItemDescriptor{Version: flag.Version, Item: flag}
				if err := insert(&result.Flags, flagCategory, key, data); err != nil {
					return mergeResult{}, err
				}
			}
		}
		if d.Segments != nil {
			for key, s := range *d.Segments {
				data := ldstoretypes.ItemDescriptor{Version: s.Version, Item: &s}
				if err := insert(&result.Segments, segmentCategory, key, data); err != nil {
					return mergeResult{}, err
				}
			}
		}
	}
	return result, nil
}

// makeFlagWithValue expands a flag-key-to-value entry into a full flag definition that returns
// the given value for every context.
func makeFlagWithValue(key string, v interface{}) *ldmodel.FeatureFlag {
	flag := ldbuilders.NewFlagBuilder(key).SingleVariation(ldvalue.CopyArbitraryValue(v)).Build()
	return &flag
}
