package cmd

import (
	"errors"

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
