package daemon

import "testing"

func TestSearchGroupsByWaitsOn(t *testing.T) { t.Skip("B8 search groups results by whom they wait on") }

func TestSearchListsViewsOfEachResult(t *testing.T) { t.Skip("B8 each result lists its rail views") }

func TestSearchFoldsAccents(t *testing.T) { t.Skip("B8 search folds accents") }

func TestSearchLimitPerGroupCountsAll(t *testing.T) {
	t.Skip("B8 a group lists its limit and counts all")
}

func TestSearchEmptyQuery(t *testing.T) { t.Skip("B8 an empty query is 400") }
