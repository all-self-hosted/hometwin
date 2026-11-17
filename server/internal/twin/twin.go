// Package twin says what the twin is for and how far it goes: the household's questions it
// is built to answer (each with the decision it improves and the data that needs), and the
// five capability levels, from describing the home to acting in it, with where each stands.
package twin

// Goal is one question the household wants answered.
type Goal struct {
	ID       string   `json:"id"`
	Question string   `json:"question"`
	Before   string   `json:"before"` // how it is decided today
	After    string   `json:"after"`  // how the twin decides it
	Data     []string `json:"data"`   // what must be modelled for that
	Level    string   `json:"level"`
}

// Level is one capability level and where the twin stands on it.
type Level struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	What   string   `json:"what"`
	Status string   `json:"status"` // done, partial, next
	Has    []string `json:"has"`
	Next   []string `json:"next,omitempty"`
}

// Goals come first: everything modelled is there to answer one of these.
var Goals = []Goal{
	{ID: "fit", Question: "买的家具放得下、搬得进吗？", Before: "拿卷尺量，凭感觉", After: "按商品真实尺寸放进户型，自动查碰撞、过道、门窗和入户门", Data: []string{"户型：房间、墙厚、门的宽度和开向、窗台高度", "商品尺寸、包装尺寸"}, Level: "descriptive"},
	{ID: "state", Question: "家里现在是什么状态？", Before: "挨个打开各家的 App", After: "一个模型上看全屋的灯、空调、门锁、窗帘和温湿度", Data: []string{"设备所在房间和位置", "设备实时状态（MQTT 主题 home/<类>/<房间>/<设备>）"}, Level: "informative"},
	{ID: "safe", Question: "门关好、锁好了吗？有没有异常？", Before: "出门后回头看一眼", After: "门开太久、没锁、湿度突然升高都会提醒", Data: []string{"门锁和门磁", "传感器读数和它最近的正常范围"}, Level: "informative"},
	{ID: "cool", Question: "现在开空调，多久能凉下来？", Before: "开了再说", After: "按房间的热模型预计到温时间，提前开", Data: []string{"房间面积", "室温、室外温度", "空调模式和设定温度"}, Level: "predictive"},
	{ID: "whatif", Question: "设 22 ℃ 还是 26 ℃ 更合适？", Before: "试一试", After: "先在模型上试算温度曲线再决定", Data: []string{"房间热模型", "空调能力"}, Level: "comprehensive"},
	{ID: "routine", Question: "出门、睡前能自动处理好吗？", Before: "每次自己挨个关", After: "按授权等级执行：只建议 / 有护栏地执行 / 自主，开锁一定要人确认", Data: []string{"设备规则和限值", "自动化和它的授权等级", "执行记录"}, Level: "autonomous"},
}

// Levels are the five capability levels.
var Levels = []Level{
	{ID: "descriptive", Name: "描述", What: "家是什么样：户型、墙门窗、家具的真实尺寸、设备在哪", Status: "done",
		Has: []string{"户型 JSON（墙厚、门宽和开向、窗台高）", "宜家链接导入真实尺寸", "按尺寸生成的家具模型", "布置检查", "家的知识图谱和本体（CortexDB：类、关系、推理规则、SHACL 约束）"}},
	{ID: "informative", Name: "信息", What: "家现在怎么样：设备状态实时同步，异常会提醒", Status: "done",
		Has: []string{"设备状态经 WebSocket 推到所有打开的页面", "MQTT 主题 home/<类>/<房间>/<设备>", "门开太久、没锁、读数突变提醒", "本体上的读数约束（卫生间湿度 > 80% 持续 2 分钟 → 提示通风）"},
		Next: []string{"接入 Home Assistant（适配器接口已留好）", "历史曲线"}},
	{ID: "predictive", Name: "预测", What: "家接下来会怎样：按模型推算", Status: "partial",
		Has:  []string{"每个房间的热模型（牛顿冷却 + 空调项），预计到温时间", "读数融合（数据同化）"},
		Next: []string{"用历史读数拟合每个房间的时间常数"}},
	{ID: "comprehensive", Name: "综合", What: "如果这样做会怎样：在模型上试不同的设定", Status: "partial",
		Has:  []string{"空调设定的温度曲线试算"},
		Next: []string{"电费和峰谷电价模拟", "家具换位前后的动线对比"}},
	{ID: "autonomous", Name: "自主", What: "家自己处理：按授权等级执行，人随时能管", Status: "partial",
		Has:  []string{"自动化分三级授权：只建议 / 有护栏 / 自主", "开锁等高风险动作必须人批准", "家庭助手（agent-go）用工具查家、控设备，高风险动作只提请批准", "每次执行都有记录"},
		Next: []string{"学习作息自动提议自动化"}},
}
