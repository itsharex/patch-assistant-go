package tree

import "cn.cpoet.patch.assistant/internal/constant"

// TotalInfo 补丁统计信息，对应 Java 的 TotalInfo。
//
// Java 侧用 JavaFX 的 IntegerProperty 是为了让界面上的数字自动刷新；
// Go 版没有这层绑定需求，用普通 int 字段 + 方法，语义完全一致。
//
// 注意 setXxxTotal 在 Java 里带一个 `if (xxx >= 0)` 的守卫：负数一律被忽略，
// 也就是"计数不会降到 0 以下"。这里照搬 —— 别顺手去掉，否则
// cleanMappedNode 的重复调用会导致计数变负，界面统计就跟 Java 版对不上了。
type TotalInfo struct {
	addTotal       int
	modTotal       int
	delTotal       int
	manualDelTotal int
}

// NewTotalInfo 创建空的统计信息。
func NewTotalInfo() *TotalInfo { return &TotalInfo{} }

// AddTotal 新增统计。
func (t *TotalInfo) AddTotal() int { return t.addTotal }

// ModTotal 更新统计。
func (t *TotalInfo) ModTotal() int { return t.modTotal }

// DelTotal 删除统计。
func (t *TotalInfo) DelTotal() int { return t.delTotal }

// ManualDelTotal 手动删除统计。
func (t *TotalInfo) ManualDelTotal() int { return t.manualDelTotal }

// SetAddTotal 设置新增统计，负数忽略（对齐 Java 的守卫）。
func (t *TotalInfo) SetAddTotal(v int) {
	if v >= 0 {
		t.addTotal = v
	}
}

// SetModTotal 设置更新统计，负数忽略。
func (t *TotalInfo) SetModTotal(v int) {
	if v >= 0 {
		t.modTotal = v
	}
}

// SetDelTotal 设置删除统计，负数忽略。
func (t *TotalInfo) SetDelTotal(v int) {
	if v >= 0 {
		t.delTotal = v
	}
}

// SetManualDelTotal 设置手动删除统计，负数忽略。
func (t *TotalInfo) SetManualDelTotal(v int) {
	if v >= 0 {
		t.manualDelTotal = v
	}
}

// IsMappedAddOrModNode 是否存在绑定的（非删除的）节点。
func (t *TotalInfo) IsMappedAddOrModNode() bool {
	return t.addTotal+t.modTotal > 0
}

// IsChangeNode 是否存在变化的节点。
func (t *TotalInfo) IsChangeNode() bool {
	return t.addTotal > 0 || t.modTotal > 0 || t.delTotal > 0 || t.manualDelTotal > 0
}

// IncrTotal 按状态累加计数。
// 对应 Java TotalInfo.incrTotal —— NONE 及其它状态不计。
func (t *TotalInfo) IncrTotal(status constant.TreeNodeType) {
	switch status {
	case constant.TreeNodeTypeAdd:
		t.SetAddTotal(t.addTotal + 1)
	case constant.TreeNodeTypeMod:
		t.SetModTotal(t.modTotal + 1)
	case constant.TreeNodeTypeDel:
		t.SetDelTotal(t.delTotal + 1)
	case constant.TreeNodeTypeManualDel:
		t.SetManualDelTotal(t.manualDelTotal + 1)
	}
}

// DecrTotal 按状态递减计数。
// 对应 Java TotalInfo.decrTotal。注意负值会被 SetXxx 的守卫吞掉，
// 即计数只会停在 0 —— 这是上游行为，照搬。
func (t *TotalInfo) DecrTotal(status constant.TreeNodeType) {
	switch status {
	case constant.TreeNodeTypeAdd:
		t.SetAddTotal(t.addTotal - 1)
	case constant.TreeNodeTypeMod:
		t.SetModTotal(t.modTotal - 1)
	case constant.TreeNodeTypeDel:
		t.SetDelTotal(t.delTotal - 1)
	case constant.TreeNodeTypeManualDel:
		t.SetManualDelTotal(t.manualDelTotal - 1)
	}
}

// CleanTotal 清空统计信息。
func (t *TotalInfo) CleanTotal() {
	t.SetAddTotal(0)
	t.SetDelTotal(0)
	t.SetModTotal(0)
	t.SetManualDelTotal(0)
}
