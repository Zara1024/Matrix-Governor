package scheduler

import (
	"fmt"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

// GangGroupStatus Gang 作业状态
type GangGroupStatus string

const (
	GangStatusPending   GangGroupStatus = "Pending"   // 等待所有成员满足
	GangStatusScheduled GangGroupStatus = "Scheduled" // 满足全进全出，准许全量调度
	GangStatusTimeout   GangGroupStatus = "Timeout"   // 超时回滚释放
)

// GangGroup 一个批处理任务的成员组
type GangGroup struct {
	Name            string
	MinMember       int           // 最少成员数 (如 4)
	CurrentMembers  []string      // 已到达的成员 Pod UID
	ScheduleTimeout time.Duration // 超时门限
	CreatedAt       time.Time
	Status          GangGroupStatus
}

// GangScheduler Gang 协同调度控制器 (Coscheduling / All-or-Nothing)
type GangScheduler struct {
	mu     sync.RWMutex
	groups map[string]*GangGroup
}

// NewGangScheduler 创建 Gang 调度器
func NewGangScheduler() *GangScheduler {
	return &GangScheduler{
		groups: make(map[string]*GangGroup),
	}
}

// RegisterGangGroup 注册一个批处理作业的 Gang 组
func (s *GangScheduler) RegisterGangGroup(name string, minMember int, timeout time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.groups[name] = &GangGroup{
		Name:            name,
		MinMember:       minMember,
		CurrentMembers:  make([]string, 0, minMember),
		ScheduleTimeout: timeout,
		CreatedAt:       time.Now(),
		Status:          GangStatusPending,
	}
	klog.Infof("[GangScheduler] 成功注册批处理 Gang 协同调度组: %s (最少成员需求: %d, 超时: %v)", name, minMember, timeout)
}

// PermitPod 调度拦截校验：仅当该组所有 Pod 均满足要求时，准许全量放行；否则挂起等待
func (s *GangScheduler) PermitPod(groupName, podUID string) (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	grp, exists := s.groups[groupName]
	if !exists {
		// 未指定 Gang 组的普通 Pod 正常放行
		return true, "普通单体应用，直接放行"
	}

	// 检查是否超时
	if time.Since(grp.CreatedAt) > grp.ScheduleTimeout {
		grp.Status = GangStatusTimeout
		return false, fmt.Sprintf("Gang 调度组 %s 超过等待门限 (%v)，触发死锁防范超时回滚！", groupName, grp.ScheduleTimeout)
	}

	grp.CurrentMembers = append(grp.CurrentMembers, podUID)

	// 校验全进全出条件：已就绪成员是否达到 MinMember 阈值
	if len(grp.CurrentMembers) >= grp.MinMember {
		grp.Status = GangStatusScheduled
		klog.Infof("[GangScheduler] 批处理 Gang 组 %s 成员已全量集齐 (%d/%d)，准许全量并发入驻调度！",
			groupName, len(grp.CurrentMembers), grp.MinMember)
		return true, "成员集齐，满足 All-or-Nothing 准许调度"
	}

	return false, fmt.Sprintf("批处理任务等待同伴中 (当前 %d/%d)，暂缓调度以防死锁", len(grp.CurrentMembers), grp.MinMember)
}
