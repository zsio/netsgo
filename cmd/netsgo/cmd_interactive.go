package main

import "netsgo/internal/tui"

func runInteractiveCommand(run func() error) error {
	if err := run(); err != nil {
		if tui.IsCancelled(err) {
			tui.PrintSummary("已取消", [][2]string{{"状态", "未进行任何修改"}})
			return nil
		}
		return err
	}
	return nil
}
