package kv

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/cometbft/cometbft/internal/test"
	"github.com/stretchr/testify/require"

	db "github.com/cometbft/cometbft-db"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cometbft/cometbft/libs/pubsub/query"
	"github.com/cometbft/cometbft/types"
)

func TestBlockerIndexer_Prune(t *testing.T) {
	store := db.NewPrefixDB(db.NewMemDB(), []byte("block_events"))
	indexer := New(store)

	events1 := getEventsForTesting(1)
	events2 := getEventsForTesting(2)

	err := indexer.Index(events1)
	require.NoError(t, err)

	keys1 := getKeys(indexer)

	err = indexer.Index(events2)
	require.NoError(t, err)

	keys2 := getKeys(indexer)

	require.True(t, subslice(keys1, keys2))

	retainedHeight, err := indexer.Prune(0, 2)
	require.NoError(t, err)
	require.Equal(t, int64(2), retainedHeight)

	keys3 := getKeys(indexer)
	require.True(t, equalSlices(sliceDiff(keys2, keys1), keys3))
	require.True(t, emptyIntersection(keys1, keys3))
}

func BenchmarkBlockerIndexer_Prune(_ *testing.B) {
	config := test.ResetTestRoot("block_indexer")
	defer func() {
		err := os.RemoveAll(config.RootDir)
		if err != nil {
			panic(err)
		}
	}()

	store, err := db.NewDB("block", db.GoLevelDBBackend, config.DBDir())
	if err != nil {
		panic(err)
	}
	indexer := New(store)

	maxHeight := 10000
	for h := 1; h < maxHeight; h++ {
		event := getEventsForTesting(int64(h))
		err := indexer.Index(event)
		if err != nil {
			panic(err)
		}
	}

	startTime := time.Now()

	for h := 1; h <= maxHeight; h++ {
		_, _ = indexer.Prune(int64(h-1), int64(h))
	}
	fmt.Println(time.Since(startTime))
}

func TestBlockIndexer(t *testing.T) {
	store := db.NewPrefixDB(db.NewMemDB(), []byte("block_events"))
	indexer := New(store)

	require.NoError(t, indexer.Index(types.EventDataNewBlockEvents{
		Height: 1,
		Events: []abci.Event{
			{
				Type: "begin_event",
				Attributes: []abci.EventAttribute{
					{
						Key:   "proposer",
						Value: "FCAA001",
						Index: true,
					},
				},
			},
			{
				Type: "end_event",
				Attributes: []abci.EventAttribute{
					{
						Key:   "foo",
						Value: "100",
						Index: true,
					},
				},
			},
		},
	}))

	for i := 2; i < 12; i++ {
		var index bool
		if i%2 == 0 {
			index = true
		}

		require.NoError(t, indexer.Index(types.EventDataNewBlockEvents{
			Height: int64(i),
			Events: []abci.Event{
				{
					Type: "begin_event",
					Attributes: []abci.EventAttribute{
						{
							Key:   "proposer",
							Value: "FCAA001",
							Index: true,
						},
					},
				},
				{
					Type: "end_event",
					Attributes: []abci.EventAttribute{
						{
							Key:   "foo",
							Value: fmt.Sprintf("%d", i),
							Index: index,
						},
					},
				},
			},
		}))
	}

	testCases := map[string]struct {
		q       *query.Query
		results []int64
	}{
		"block.height = 100": {
			q:       query.MustCompile(`block.height = 100`),
			results: []int64{},
		},
		"block.height = 5": {
			q:       query.MustCompile(`block.height = 5`),
			results: []int64{5},
		},
		"begin_event.key1 = 'value1'": {
			q:       query.MustCompile(`begin_event.key1 = 'value1'`),
			results: []int64{},
		},
		"begin_event.proposer = 'FCAA001'": {
			q:       query.MustCompile(`begin_event.proposer = 'FCAA001'`),
			results: []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11},
		},
		"end_event.foo <= 5": {
			q:       query.MustCompile(`end_event.foo <= 5`),
			results: []int64{2, 4},
		},
		"end_event.foo >= 100": {
			q:       query.MustCompile(`end_event.foo >= 100`),
			results: []int64{1},
		},
		"block.height > 2 AND end_event.foo <= 8": {
			q:       query.MustCompile(`block.height > 2 AND end_event.foo <= 8`),
			results: []int64{4, 6, 8},
		},
		"end_event.foo > 100": {
			q:       query.MustCompile("end_event.foo > 100"),
			results: []int64{},
		},
		"block.height >= 2 AND end_event.foo < 8": {
			q:       query.MustCompile("block.height >= 2 AND end_event.foo < 8"),
			results: []int64{2, 4, 6},
		},
		"begin_event.proposer CONTAINS 'FFFFFFF'": {
			q:       query.MustCompile(`begin_event.proposer CONTAINS 'FFFFFFF'`),
			results: []int64{},
		},
		"begin_event.proposer CONTAINS 'FCAA001'": {
			q:       query.MustCompile(`begin_event.proposer CONTAINS 'FCAA001'`),
			results: []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11},
		},
		"end_event.foo CONTAINS '1'": {
			q:       query.MustCompile("end_event.foo CONTAINS '1'"),
			results: []int64{1, 10},
		},
	}

	for name, tc := range testCases {
		tc := tc
		t.Run(name, func(t *testing.T) {
			results, err := indexer.Search(context.Background(), tc.q)
			require.NoError(t, err)
			require.Equal(t, tc.results, results)
		})
	}
}

func TestBlockIndexerMulti(t *testing.T) {
	store := db.NewPrefixDB(db.NewMemDB(), []byte("block_events"))
	indexer := New(store)

	require.NoError(t, indexer.Index(types.EventDataNewBlockEvents{
		Height: 1,
		Events: []abci.Event{
			{},
			{
				Type: "end_event",
				Attributes: []abci.EventAttribute{
					{
						Key:   "foo",
						Value: "100",
						Index: true,
					},
					{
						Key:   "bar",
						Value: "200",
						Index: true,
					},
				},
			},
			{
				Type: "end_event",
				Attributes: []abci.EventAttribute{
					{
						Key:   "foo",
						Value: "300",
						Index: true,
					},
					{
						Key:   "bar",
						Value: "500",
						Index: true,
					},
				},
			},
		},
	}))

	require.NoError(t, indexer.Index(types.EventDataNewBlockEvents{
		Height: 2,
		Events: []abci.Event{
			{},
			{
				Type: "end_event",
				Attributes: []abci.EventAttribute{
					{
						Key:   "foo",
						Value: "100",
						Index: true,
					},
					{
						Key:   "bar",
						Value: "200",
						Index: true,
					},
				},
			},
			{
				Type: "end_event",
				Attributes: []abci.EventAttribute{
					{
						Key:   "foo",
						Value: "300",
						Index: true,
					},
					{
						Key:   "bar",
						Value: "400",
						Index: true,
					},
				},
			},
		},
	}))

	testCases := map[string]struct {
		q       *query.Query
		results []int64
	}{

		"query return all events from a height - exact": {
			q:       query.MustCompile("block.height = 1"),
			results: []int64{1},
		},
		"query return all events from a height - exact (deduplicate height)": {
			q:       query.MustCompile("block.height = 1 AND block.height = 2"),
			results: []int64{1},
		},
		"query return all events from a height - range": {
			q:       query.MustCompile("block.height < 2 AND block.height > 0 AND block.height > 0"),
			results: []int64{1},
		},
		"query return all events from a height - range 2": {
			q:       query.MustCompile("block.height < 3 AND block.height < 2 AND block.height > 0 AND block.height > 0"),
			results: []int64{1},
		},
		"query return all events from a height - range 3": {
			q:       query.MustCompile("block.height < 1 AND block.height > 1"),
			results: []int64{},
		},
		"query matches fields from same event": {
			q:       query.MustCompile("end_event.bar < 300 AND end_event.foo = 100 AND block.height > 0 AND block.height <= 2"),
			results: []int64{1, 2},
		},
		"query matches fields from multiple events": {
			q:       query.MustCompile("end_event.foo = 100 AND end_event.bar = 400 AND block.height = 2"),
			results: []int64{},
		},
		"query matches fields from multiple events 2": {
			q:       query.MustCompile("end_event.foo = 100 AND end_event.bar > 200 AND block.height > 0 AND block.height < 3"),
			results: []int64{},
		},
		"query matches fields from multiple events allowed": {
			q:       query.MustCompile("end_event.foo = 100 AND end_event.bar = 400"),
			results: []int64{},
		},
		"query matches fields from all events whose attribute is within range": {
			q:       query.MustCompile("block.height  = 2 AND end_event.foo < 300"),
			results: []int64{2},
		},
		"match attributes across events with height constraint": {
			q:       query.MustCompile("end_event.foo = 100 AND end_event.bar = 400 AND block.height = 2"),
			results: []int64{},
		},
		"query using CONTAINS matches fields from all events whose attribute is within range": {
			q:       query.MustCompile("block.height  = 2 AND end_event.foo CONTAINS '30'"),
			results: []int64{2},
		},
		"query matches all fields from multiple events": {
			q:       query.MustCompile("end_event.bar > 100 AND end_event.bar <= 500"),
			results: []int64{1, 2},
		},
		"query with height range and height equality - should ignore equality": {
			q:       query.MustCompile("block.height = 2 AND end_event.foo >= 100 AND block.height < 2"),
			results: []int64{1},
		},
		"query with non-existent field": {
			q:       query.MustCompile("end_event.baz = 100"),
			results: []int64{},
		},
		"query with non-existent field ": {
			q:       query.MustCompile("end_event.baz = 100"),
			results: []int64{},
		},
	}

	for name, tc := range testCases {
		tc := tc
		t.Run(name, func(t *testing.T) {
			results, err := indexer.Search(context.Background(), tc.q)
			require.NoError(t, err)
			require.Equal(t, tc.results, results)
		})
	}
}

func TestBigInt(t *testing.T) {

	bigInt := "10000000000000000000"
	bigFloat := bigInt + ".76"
	bigFloatLower := bigInt + ".1"
	bigIntSmaller := "9999999999999999999"
	store := db.NewPrefixDB(db.NewMemDB(), []byte("block_events"))
	indexer := New(store)

	require.NoError(t, indexer.Index(types.EventDataNewBlockEvents{
		Height: 1,
		Events: []abci.Event{
			{},
			{
				Type: "end_event",
				Attributes: []abci.EventAttribute{
					{
						Key:   "foo",
						Value: "100",
						Index: true,
					},
					{
						Key:   "bar",
						Value: bigFloat,
						Index: true,
					},
					{
						Key:   "bar_lower",
						Value: bigFloatLower,
						Index: true,
					},
				},
			},
			{
				Type: "end_event",
				Attributes: []abci.EventAttribute{
					{
						Key:   "foo",
						Value: bigInt,
						Index: true,
					},
					{
						Key:   "bar",
						Value: "500",
						Index: true,
					},
					{
						Key:   "bla",
						Value: "500.5",
						Index: true,
					},
				},
			},
		},
	},
	))

	testCases := map[string]struct {
		q       *query.Query
		results []int64
	}{

		"query return all events from a height - exact": {
			q:       query.MustCompile("block.height = 1"),
			results: []int64{1},
		},
		"query return all events from a height - exact (deduplicate height)": {
			q:       query.MustCompile("block.height = 1 AND block.height = 2"),
			results: []int64{1},
		},
		"query return all events from a height - range": {
			q:       query.MustCompile("block.height < 2 AND block.height > 0 AND block.height > 0"),
			results: []int64{1},
		},
		"query matches fields with big int and height - no match": {
			q:       query.MustCompile("end_event.foo = " + bigInt + " AND end_event.bar = 500 AND block.height = 2"),
			results: []int64{},
		},
		"query matches fields with big int with less and height - no match": {
			q:       query.MustCompile("end_event.foo <= " + bigInt + " AND end_event.bar = 500 AND block.height = 2"),
			results: []int64{},
		},
		"query matches fields with big int and height - match": {
			q:       query.MustCompile("end_event.foo = " + bigInt + " AND end_event.bar = 500 AND block.height = 1"),
			results: []int64{1},
		},
		"query matches big int in range": {
			q:       query.MustCompile("end_event.foo = " + bigInt),
			results: []int64{1},
		},
		"query matches big int in range with float with equality ": {
			q:       query.MustCompile("end_event.bar >= " + bigInt),
			results: []int64{1},
		},
		"query matches big int in range with float ": {
			q:       query.MustCompile("end_event.bar > " + bigInt),
			results: []int64{1},
		},
		"query matches big int in range with float lower dec point ": {
			q:       query.MustCompile("end_event.bar_lower > " + bigInt),
			results: []int64{1},
		},
		"query matches big int in range with float with less - found": {
			q:       query.MustCompile("end_event.foo <= " + bigInt),
			results: []int64{1},
		},
		"query matches big int in range with float with less with height range - found": {
			q:       query.MustCompile("end_event.foo <= " + bigInt + " AND block.height > 0"),
			results: []int64{1},
		},
		"query matches big int in range with float with less - not found": {
			q:       query.MustCompile("end_event.foo < " + bigInt + " AND end_event.foo > 100"),
			results: []int64{},
		},
		"query does not parse float": {
			q:       query.MustCompile("end_event.bla >= 500"),
			results: []int64{1},
		},
		"query condition float": {
			q:       query.MustCompile("end_event.bla < " + bigFloat),
			results: []int64{1},
		},
		"query condition big int plus one": {
			q:       query.MustCompile("end_event.foo > " + bigIntSmaller),
			results: []int64{1},
		},
	}
	for name, tc := range testCases {
		tc := tc
		t.Run(name, func(t *testing.T) {
			results, err := indexer.Search(context.Background(), tc.q)
			require.NoError(t, err)
			require.Equal(t, tc.results, results)
		})
	}
}

func TestKeyArray(t *testing.T) {
	key1 := []byte("a")
	key2 := []byte("ab")
	key3 := []byte("abacabadabacaba")

	var keyArray []byte
	keyArray = appendToKeyArray(keyArray, key1)
	keyArray = appendToKeyArray(keyArray, key2)
	keyArray = appendToKeyArray(keyArray, key3)

	keys := getKeysFromKeyArray(keyArray)

	require.Equal(t, len(keys), 3)
	require.Equal(t, keys[0], key1)
	require.Equal(t, keys[1], key2)
	require.Equal(t, keys[2], key3)
}

func getEventsForTesting(height int64) types.EventDataNewBlockEvents {
	return types.EventDataNewBlockEvents{
		Height: height,
		Events: []abci.Event{
			{
				Type: "begin_event",
				Attributes: []abci.EventAttribute{
					{
						Key:   "proposer",
						Value: "FCAA001",
						Index: true,
					},
				},
			},
			{
				Type: "end_event",
				Attributes: []abci.EventAttribute{
					{
						Key:   "foo",
						Value: "100",
						Index: true,
					},
				},
			},
		},
	}
}

func getKeys(indexer *BlockerIndexer) [][]byte {
	var keys [][]byte
	itr, err := indexer.store.Iterator(nil, nil)
	if err != nil {
		panic(err)
	}
	for ; itr.Valid(); itr.Next() {
		keys = append(keys, itr.Key())
	}
	return keys
}

func equal(x []byte, y []byte) bool {
	if len(x) != len(y) {
		return false
	}
	for i, elem := range x {
		if elem != y[i] {
			return false
		}
	}
	return true
}

func contains(slice [][]byte, target []byte) bool {
	for _, element := range slice {
		if equal(element, target) {
			return true
		}
	}
	return false
}

func subslice(smaller [][]byte, bigger [][]byte) bool {
	for _, elem := range smaller {
		if !contains(bigger, elem) {
			return false
		}
	}
	return true
}

func equalSlices(x [][]byte, y [][]byte) bool {
	return subslice(x, y) && subslice(y, x)
}

func emptyIntersection(x [][]byte, y [][]byte) bool {
	for _, elem := range x {
		if contains(y, elem) {
			return false
		}
	}
	return true
}

func sliceDiff(bigger [][]byte, smaller [][]byte) [][]byte {
	var diff [][]byte
	for _, elem := range bigger {
		if !contains(smaller, elem) {
			diff = append(diff, elem)
		}
	}
	return diff
}
