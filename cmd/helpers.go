package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

const sizeMax = 1000

func validateSize(size int) (int, bool, error) {
	if size < 1 {
		return 0, false, errors.New("size must be at least 1")
	}
	if size > sizeMax {
		return sizeMax, true, nil
	}
	return size, false, nil
}

func requireSize(cmd *cobra.Command) (int, bool, error) {
	raw, _ := cmd.Flags().GetInt("size")
	size, capped, err := validateSize(raw)
	if err != nil {
		return 0, false, failValidation(err.Error())
	}
	return size, capped, nil
}

func requireSearchPage(cmd *cobra.Command) (limit, offset int, capped bool, err error) {
	sizeRaw, _ := cmd.Flags().GetInt("size")
	limitRaw, _ := cmd.Flags().GetInt("limit")
	sizeSet := cmd.Flags().Changed("size")
	limitSet := cmd.Flags().Changed("limit")
	if sizeSet && limitSet && sizeRaw != limitRaw {
		return 0, 0, false, failValidation("--limit and --size cannot differ; use --limit for Agent calls")
	}
	raw := sizeRaw
	if limitSet {
		raw = limitRaw
	}
	limit, capped, valErr := validateSize(raw)
	if valErr != nil {
		return 0, 0, false, failValidation(strings.Replace(valErr.Error(), "size", "limit", 1))
	}
	offset, err = requireOffset(cmd)
	if err != nil {
		return 0, 0, false, err
	}
	return limit, offset, capped, nil
}

func requireBucketLimit(cmd *cobra.Command) (int, error) {
	buckets, _ := cmd.Flags().GetInt("buckets")
	limit, _ := cmd.Flags().GetInt("limit")
	bucketsSet := cmd.Flags().Changed("buckets")
	limitSet := cmd.Flags().Changed("limit")
	if bucketsSet && limitSet && buckets != limit {
		return 0, failValidation("--limit and --buckets cannot differ; use --limit for Agent calls")
	}
	if limitSet {
		buckets = limit
	}
	if buckets < 1 || buckets > 100 {
		return 0, failValidation("--limit must be 1-100")
	}
	return buckets, nil
}

func requireOffset(cmd *cobra.Command) (int, error) {
	offset, _ := cmd.Flags().GetInt("offset")
	if offset < 0 {
		return 0, failValidation("--offset must be 0 or greater")
	}
	return offset, nil
}

func addLimitOffsetFlags(cmd *cobra.Command, defaultLimit int, limitUsage string) {
	cmd.Flags().Int("limit", defaultLimit, limitUsage)
	cmd.Flags().Int("offset", 0, "Zero-based result offset for pagination")
}

func pageMeta(total, count, limit, offset int) map[string]any {
	hasMore := offset+count < total
	var next any
	if hasMore {
		next = offset + count
	}
	return map[string]any{
		"count":       count,
		"total":       total,
		"limit":       limit,
		"offset":      offset,
		"next_offset": next,
		"has_more":    hasMore,
	}
}

func paginateSlice[T any](items []T, limit, offset int) ([]T, map[string]any, error) {
	if limit < 1 {
		return nil, nil, fmt.Errorf("--limit must be at least 1")
	}
	if offset < 0 {
		return nil, nil, fmt.Errorf("--offset must be 0 or greater")
	}
	total := len(items)
	if offset >= total {
		return nil, pageMeta(total, 0, limit, offset), nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	page := items[offset:end]
	return page, pageMeta(total, len(page), limit, offset), nil
}
