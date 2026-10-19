package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/mritd/goadmission/pkg/adfunc"
	"github.com/mritd/goadmission/pkg/conf"
	"github.com/mritd/goadmission/pkg/nodemanager"
	"github.com/mritd/goadmission/pkg/protector"
	"github.com/mritd/goadmission/pkg/route"
	"github.com/mritd/goadmission/pkg/zaplogger"
	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var (
	version   = "v1.2.0"
	buildDate string
	commitID  string

	versionTpl = `
============================================================
       __  ___      __       _         ______                                    
      /  |/  /___ _/ /______(_)  __   / ____/___ _   _____  _________  ____  _____
     / /|_/ / __ ` + "`" + `/ __/ ___/ / |/_/  / / __/ __ \ | / / _ \/ ___/ __ \/ __ \/ ___/
    / /  / / /_/ / /_/ /  / />  <   / /_/ / /_/ / |/ /  __/ /  / / / / /_/ / /    
   /_/  /_/\__,_/\__/_/  /_/_/|_|   \____/\____/|___/\___/_/  /_/ /_/\____/_/     
============================================================
Name: Matrix-Governor (Kubernetes Dynamic Oversold & Protection Governor)
Version: %s
Arch: %s
BuildDate: %s
CommitID: %s
`
)

var rootCmd = &cobra.Command{
	Use:     "matrix-governor",
	Short:   "Kubernetes Dynamic Oversold & Protection Governor (PID Controller + PDB Eviction Scorer)",
	Version: version,
	Run: func(cmd *cobra.Command, args []string) {
		zaplogger.Setup()
		adfunc.Setup()
		route.Setup()

		logger := zaplogger.NewSugar("main")

		// 启动 Matrix-Governor 后台动态协调管理器 (PID 计算 + 节点水位探测巡检)
		mgr := nodemanager.GetGlobalManager()
		mgr.StartAutoSync(5 * time.Second)
		logger.Info("Matrix-Governor 节点超卖协调与 PID 闭环计算后台服务已成功启动！")

		// 启动 Auto-PDB 自动化防护控制器 (若运行在集群中)
		var pdbCtrl *protector.AutoPDBController
		k8sConfig, err := rest.InClusterConfig()
		if err == nil {
			kubeClient, err := kubernetes.NewForConfig(k8sConfig)
			if err == nil {
				// 自动发现集群节点纳管并初始化指标上报
				mgr.AutoDiscoverNodes(kubeClient)
				logger.Info("Matrix-Governor 已完成集群纳管节点自发现与初始状态上报")

				pdbCtrl = protector.NewAutoPDBController(kubeClient)
				pdbCtrl.Start(context.Background(), 5*time.Second)
				logger.Info("Matrix-Governor 企业级 Auto-PDB 自动化防护控制器已在后台启动 (巡检周期: 5s)")
			} else {
				logger.Warnf("初始化 K8s In-Cluster 客户端失败: %v", err)
			}
		} else {
			logger.Infof("未检测到 In-Cluster KubeConfig，Auto-PDB 控制器跳过启动 (非容器集群内模式)")
		}

		srv := &http.Server{
			Handler: route.Router(),
			Addr:    conf.Addr,
		}

		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			var shutdownOnce sync.Once
			for range sigs {
				logger.Warn("接收到退出中断信号，开始优雅终止 Matrix-Governor...")
				shutdownOnce.Do(func() {
					mgr.Stop()
					if pdbCtrl != nil {
						pdbCtrl.Stop()
					}
					err := srv.Shutdown(context.Background())
					if err != nil {
						logger.Error(err)
					}
				})
			}
		}()

		if conf.Cert != "" && conf.Key != "" {
			logger.Infof("Matrix-Governor Webhook TLS 监听已就绪: %s", conf.Addr)
			err := srv.ListenAndServeTLS(conf.Cert, conf.Key)
			if err != nil {
				if err == http.ErrServerClosed {
					logger.Info("Matrix-Governor 服务安全平稳关闭成功。")
				} else {
					logger.Fatal(err)
				}
			}
		} else {
			logger.Infof("Matrix-Governor HTTP 监听已就绪: %s (测试模式)", conf.Addr)
			err := srv.ListenAndServe()
			if err != nil {
				if err == http.ErrServerClosed {
					logger.Info("Matrix-Governor 服务安全平稳关闭成功。")
				} else {
					logger.Fatal(err)
				}
			}
		}
	},
}

func init() {
	// zap 日志选项
	rootCmd.PersistentFlags().BoolVar(&zaplogger.Config.Development, "zap-devel", false, "启用 zap 开发者模式")
	rootCmd.PersistentFlags().StringVar(&zaplogger.Config.Encoder, "zap-encoder", "console", "日志格式 ('json' 或 'console')")
	rootCmd.PersistentFlags().StringVar(&zaplogger.Config.Level, "zap-level", "info", "日志级别 ('debug', 'info', 'warn', 'error')")
	rootCmd.PersistentFlags().BoolVar(&zaplogger.Config.Sample, "zap-sample", false, "是否开启日志采样")
	rootCmd.PersistentFlags().StringVar(&zaplogger.Config.TimeEncoding, "zap-time-encoding", "default", "时间编码 ('default', 'iso8601')")
	rootCmd.PersistentFlags().StringVar(&zaplogger.Config.StackLevel, "zap-stacktrace-level", "error", "打印堆栈的最底日志级别")

	// 版本信息模板
	rootCmd.SetVersionTemplate(fmt.Sprintf(versionTpl, version, runtime.GOOS+"/"+runtime.GOARCH, buildDate, commitID))

	// Webhook 服务监听配置
	rootCmd.PersistentFlags().StringVarP(&conf.Addr, "listen", "l", ":8443", "Webhook 准入服务监听地址端口")
	rootCmd.PersistentFlags().StringVar(&conf.Cert, "cert", "", "Webhook TLS 证书路径")
	rootCmd.PersistentFlags().StringVar(&conf.Key, "key", "", "Webhook TLS 密钥路径")

	// 规则配置选项
	rootCmd.PersistentFlags().StringSliceVar(&conf.ImageRename, "image-rename", conf.DefaultImageRenameRules, "镜像重命名规则")
	rootCmd.PersistentFlags().StringSliceVar(&conf.AllowDeployTime, "allow-deploy-time", conf.DefaultAllowDeployTime, "允许发布时间窗口")
	rootCmd.PersistentFlags().StringVar(&conf.ForceDeployLabel, "force-deploy-label", conf.DefaultForceDeployLabel, "强制发布标签")
	rootCmd.PersistentFlags().StringVar(&conf.ForceEnableServiceLinksLabel, "force-enable-service-links-label", conf.DefaultForceEnableServiceLinksLabel, "强制开启服务链接标签")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
