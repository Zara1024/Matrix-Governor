package pid

import (
	"math"
	"sync"
	"time"
)

// Config 定义 PID 控制器的调优与边界参数
type Config struct {
	// TargetUtilization 目标安全利用率设定值 (例如 0.65 表示物理利用率目标为 65%)
	TargetUtilization float64
	// Kp 比例增益 (响应当前误差的速度)
	Kp float64
	// Ki 积分增益 (消除历史累计稳态误差)
	Ki float64
	// Kd 微分增益 (抑制突发负载变化率，提供阻尼防冲顶)
	Kd float64
	// MinRatio 允许的最小超卖比 (通常为 1.0，即 Allocatable == Capacity 不超卖)
	MinRatio float64
	// MaxRatio 允许的最大超卖比上限 (例如 2.0，即最多超卖 100%)
	MaxRatio float64
	// Deadband 误差死区阈值 (例如 0.02 表示 |误差| <= 2% 时不调整，防止微小抖动频繁扰动集群)
	Deadband float64
	// IntegralLimit 积分项限幅 (防止积分饱和导致控制迟钝)
	IntegralLimit float64
}

// DefaultConfig 返回适用于 Kubernetes 生产节点的推荐默认 PID 配置
func DefaultConfig() Config {
	return Config{
		TargetUtilization: 0.65, // 目标安全利用率 65%
		Kp:                0.8,  // 比例增益
		Ki:                0.15, // 积分增益
		Kd:                0.08, // 微分增益
		MinRatio:          1.0,  // 最低超卖比 1.0 (不超卖)
		MaxRatio:          2.0,  // 最高超卖比 2.0 (最大放大 2 倍)
		Deadband:          0.02, // 2% 的死区控制，防止频繁抖动
		IntegralLimit:     0.5,  // 积分限幅
	}
}

// Controller PID 自适应控制器
type Controller struct {
	mu           sync.Mutex
	cfg          Config
	currentRatio float64   // 当前输出的动态超卖比
	integral     float64   // 历史误差累积积分值
	lastError    float64   // 上一次周期的误差
	lastTime     time.Time // 上一次计算的时间戳
}

// NewController 创建一个自适应 PID 控制器实例
func NewController(cfg Config) *Controller {
	if cfg.MinRatio <= 0 {
		cfg.MinRatio = 1.0
	}
	if cfg.MaxRatio < cfg.MinRatio {
		cfg.MaxRatio = cfg.MinRatio
	}
	return &Controller{
		cfg:          cfg,
		currentRatio: cfg.MaxRatio, // 默认以 2.0x 最大基准超卖比平稳启动，遇到负载上涨时自适应向下收敛
		lastTime:     time.Now(),
	}
}

// Update 根据当前节点的实际物理利用率 (currentUsage: 0.0 ~ 1.0) 计算最新的超卖比
func (c *Controller) Update(currentUsage float64) float64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	dt := now.Sub(c.lastTime).Seconds()
	if dt <= 0 {
		dt = 1.0
	}
	c.lastTime = now

	// 误差计算：目标利用率 - 当前实际利用率
	// 如果当前利用率低于目标 (例如使用率 30%，目标 65%)，error 为正数 (+0.35)，说明空间富余，应放大超卖比
	// 如果当前利用率高于目标 (例如使用率 80%，目标 65%)，error 为负数 (-0.15)，说明已过载，应收缩超卖比
	errVal := c.cfg.TargetUtilization - currentUsage

	// 1. 防抖死区判断：如果误差在死区范围内，保持当前超卖比不变，避免微小扰动频繁改动
	if math.Abs(errVal) <= c.cfg.Deadband {
		return c.currentRatio
	}

	// 2. 比例项 (P 项)
	pTerm := c.cfg.Kp * errVal

	// 3. 积分项 (I 项) 与抗积分饱和保护 (Anti-Windup)
	c.integral += errVal * dt
	if c.cfg.IntegralLimit > 0 {
		if c.integral > c.cfg.IntegralLimit {
			c.integral = c.cfg.IntegralLimit
		} else if c.integral < -c.cfg.IntegralLimit {
			c.integral = -c.cfg.IntegralLimit
		}
	}
	iTerm := c.cfg.Ki * c.integral

	// 4. 微分项 (D 项，阻尼预测变化速率)
	derivative := (errVal - c.lastError) / dt
	dTerm := c.cfg.Kd * derivative
	c.lastError = errVal

	// 5. 综合计算 PID 调整量
	delta := pTerm + iTerm + dTerm

	// 6. 步进平滑更新超卖比
	newRatio := c.currentRatio + delta

	// 7. 输出限幅：严格限制在 [MinRatio, MaxRatio] 区间内
	if newRatio < c.cfg.MinRatio {
		newRatio = c.cfg.MinRatio
	}
	if newRatio > c.cfg.MaxRatio {
		newRatio = c.cfg.MaxRatio
	}

	c.currentRatio = math.Round(newRatio*100) / 100 // 保留两位小数
	return c.currentRatio
}

// GetCurrentRatio 获取当前生效的超卖比
func (c *Controller) GetCurrentRatio() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.currentRatio
}

// Reset 重置 PID 内部状态
func (c *Controller) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.currentRatio = c.cfg.MinRatio
	c.integral = 0
	c.lastError = 0
	c.lastTime = time.Now()
}
