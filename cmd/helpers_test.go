package cmd

import (
	"errors"
	"testing"
)

func TestValidateSize(t *testing.T) {
	tests := []struct {
		name    string
		size    int
		want    int
		capped  bool
		wantErr bool
	}{
		{name: "ok", size: 50, want: 50},
		{name: "cap", size: 2000, want: sizeMax, capped: true},
		{name: "min", size: 0, wantErr: true},
		{name: "negative", size: -1, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, capped, err := validateSize(tc.size)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want || capped != tc.capped {
				t.Fatalf("validateSize(%d) = (%d, %v), want (%d, %v)", tc.size, got, capped, tc.want, tc.capped)
			}
		})
	}
}

func TestRequireSize_Invalid(t *testing.T) {
	resetCLIState(t)
	_ = searchCmd.Flags().Set("size", "0")
	_, _, err := requireSize(searchCmd)
	if err == nil || !errors.Is(err, ErrSilent) {
		t.Fatalf("expected ErrSilent validation, got %v", err)
	}
	if lastExit != ExitBadArgs {
		t.Fatalf("exit %d want %d", lastExit, ExitBadArgs)
	}
}

func TestRequireSize_Capped(t *testing.T) {
	resetCLIState(t)
	_ = searchCmd.Flags().Set("size", "5000")
	size, capped, err := requireSize(searchCmd)
	if err != nil {
		t.Fatal(err)
	}
	if size != sizeMax || !capped {
		t.Fatalf("got size=%d capped=%v", size, capped)
	}
}

func TestRequireSearchPage_LimitOffset(t *testing.T) {
	resetCLIState(t)
	_ = searchCmd.Flags().Set("limit", "25")
	_ = searchCmd.Flags().Set("offset", "50")
	limit, offset, capped, err := requireSearchPage(searchCmd)
	if err != nil {
		t.Fatal(err)
	}
	if limit != 25 || offset != 50 || capped {
		t.Fatalf("limit=%d offset=%d capped=%v", limit, offset, capped)
	}
}

func TestRequireSearchPage_DifferentSizeAndLimit(t *testing.T) {
	resetCLIState(t)
	_ = searchCmd.Flags().Set("size", "20")
	_ = searchCmd.Flags().Set("limit", "25")
	_, _, _, err := requireSearchPage(searchCmd)
	if err == nil || !errors.Is(err, ErrSilent) {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestPaginateSlice(t *testing.T) {
	page, meta, err := paginateSlice([]string{"a", "b", "c"}, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0] != "b" || meta["has_more"] != false {
		t.Fatalf("page=%v meta=%v", page, meta)
	}
}
