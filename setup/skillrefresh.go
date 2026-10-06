package setup

import (
	"github.com/vogo/vage/tool"
	"github.com/vogo/vv/dispatches"
)

type skillSchemaRefresher struct {
	reg   tool.ToolRegistry
	use   *dispatches.UseSkillTool
	spawn *dispatches.SpawnWorkerTool
}

func (r *skillSchemaRefresher) Refresh() error {
	if r == nil {
		return nil
	}
	if r.use != nil {
		if err := r.use.Refresh(r.reg); err != nil {
			return err
		}
	}
	if r.spawn != nil {
		if err := r.spawn.Refresh(r.reg); err != nil {
			return err
		}
	}
	return nil
}
