// SPDX-License-Identifier: Apache-2.0

package project

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
)

func readBundleIndex(file string, members []string) (map[string]string, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxMetaSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxMetaSize {
		return nil, fmt.Errorf("%s: index exceeds %d bytes", file, maxMetaSize)
	}
	var index map[string]string
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("%s: reading model index: %w", file, err)
	}
	if index == nil {
		return nil, fmt.Errorf("%s: model index must be a JSON object", file)
	}
	for _, name := range slices.Sorted(maps.Keys(index)) {
		member := index[name]
		_, found := slices.BinarySearch(members, member)
		if name == "" || !found || !IsModelFile(member) {
			return nil, fmt.Errorf("%s: index entry %q must name a model file in the archive, got %q", file, name, member)
		}
	}
	return index, nil
}
