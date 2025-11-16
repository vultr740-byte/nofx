package main

import (
	"encoding/json"
	"fmt"
	"log"
	"nofx/api"
	"nofx/auth"
	"nofx/config"
	"nofx/crypto"
	"nofx/manager"
	"nofx/market"
	"nofx/pool"
	"nofx/telegram"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/joho/godotenv"
)

// ConfigFile 配置文件结构，只包含需要同步到数据库的字段
// TODO 现在与config.Config相同，未来会被替换， 现在为了兼容性不得不保留当前文件
type ConfigFile struct {
	BetaMode           bool                  `json:"beta_mode"`
	APIServerPort      int                   `json:"api_server_port"`
	UseDefaultCoins    bool                  `json:"use_default_coins"`
	DefaultCoins       []string              `json:"default_coins"`
	CoinPoolAPIURL     string                `json:"coin_pool_api_url"`
	OITopAPIURL        string                `json:"oi_top_api_url"`
	MaxDailyLoss       float64               `json:"max_daily_loss"`
	MaxDrawdown        float64               `json:"max_drawdown"`
	StopTradingMinutes int                   `json:"stop_trading_minutes"`
	Leverage           config.LeverageConfig `json:"leverage"`
	JWTSecret          string                `json:"jwt_secret"`
	DataKLineTime      string                `json:"data_k_line_time"`
	Log                *config.LogConfig     `json:"log"` // 日志配置
}

// loadConfigFile 读取并解析config.json文件
func loadConfigFile() (*ConfigFile, error) {
	// 检查config.json是否存在
	if _, err := os.Stat("config.json"); os.IsNotExist(err) {
		log.Printf("📄 config.json不存在，使用默认配置")
		return &ConfigFile{}, nil
	}

	// 读取config.json
	data, err := os.ReadFile("config.json")
	if err != nil {
		return nil, fmt.Errorf("读取config.json失败: %w", err)
	}

	// 解析JSON
	var configFile ConfigFile
	if err := json.Unmarshal(data, &configFile); err != nil {
		return nil, fmt.Errorf("解析config.json失败: %w", err)
	}

	return &configFile, nil
}

// syncConfigToDatabase 将配置同步到数据库
func syncConfigToDatabase(database *config.Database, configFile *ConfigFile) error {
	if configFile == nil {
		return nil
	}

	log.Printf("🔄 开始同步config.json到数据库...")

	// 同步各配置项到数据库
	configs := map[string]string{
		"beta_mode":            fmt.Sprintf("%t", configFile.BetaMode),
		"api_server_port":      strconv.Itoa(configFile.APIServerPort),
		"use_default_coins":    fmt.Sprintf("%t", configFile.UseDefaultCoins),
		"coin_pool_api_url":    configFile.CoinPoolAPIURL,
		"oi_top_api_url":       configFile.OITopAPIURL,
		"max_daily_loss":       fmt.Sprintf("%.1f", configFile.MaxDailyLoss),
		"max_drawdown":         fmt.Sprintf("%.1f", configFile.MaxDrawdown),
		"stop_trading_minutes": strconv.Itoa(configFile.StopTradingMinutes),
	}

	// 同步default_coins（转换为JSON字符串存储）
	if len(configFile.DefaultCoins) > 0 {
		defaultCoinsJSON, err := json.Marshal(configFile.DefaultCoins)
		if err == nil {
			configs["default_coins"] = string(defaultCoinsJSON)
		}
	}

	// 同步杠杆配置
	if configFile.Leverage.BTCETHLeverage > 0 {
		configs["btc_eth_leverage"] = strconv.Itoa(configFile.Leverage.BTCETHLeverage)
	}
	if configFile.Leverage.AltcoinLeverage > 0 {
		configs["altcoin_leverage"] = strconv.Itoa(configFile.Leverage.AltcoinLeverage)
	}

	// 如果JWT密钥不为空，也同步
	if configFile.JWTSecret != "" {
		configs["jwt_secret"] = configFile.JWTSecret
	}

	// 更新数据库配置
	for key, value := range configs {
		if err := database.SetSystemConfig(key, value); err != nil {
			log.Printf("⚠️  更新配置 %s 失败: %v", key, err)
		} else {
			log.Printf("✓ 同步配置: %s = %s", key, value)
		}
	}

	log.Printf("✅ config.json同步完成")
	return nil
}

// loadBetaCodesToDatabase 加载内测码文件到数据库
func loadBetaCodesToDatabase(database *config.Database) error {
	betaCodeFile := "beta_codes.txt"

	// 检查内测码文件是否存在
	if _, err := os.Stat(betaCodeFile); os.IsNotExist(err) {
		log.Printf("📄 内测码文件 %s 不存在，跳过加载", betaCodeFile)
		return nil
	}

	// 获取文件信息
	fileInfo, err := os.Stat(betaCodeFile)
	if err != nil {
		return fmt.Errorf("获取内测码文件信息失败: %w", err)
	}

	log.Printf("🔄 发现内测码文件 %s (%.1f KB)，开始加载...", betaCodeFile, float64(fileInfo.Size())/1024)

	// 加载内测码到数据库
	err = database.LoadBetaCodesFromFile(betaCodeFile)
	if err != nil {
		return fmt.Errorf("加载内测码失败: %w", err)
	}

	// 显示统计信息
	total, used, err := database.GetBetaCodeStats()
	if err != nil {
		log.Printf("⚠️  获取内测码统计失败: %v", err)
	} else {
		log.Printf("✅ 内测码加载完成: 总计 %d 个，已使用 %d 个，剩余 %d 个", total, used, total-used)
	}

	return nil
}

func main() {
	fmt.Println("╔════════════════════════════════════════════════════════════╗")
	fmt.Println("║    🤖 AI多模型交易系统 - 支持 DeepSeek & Qwen            ║")
	fmt.Println("╚════════════════════════════════════════════════════════════╝")
	fmt.Println()

	// Load environment variables from .env file if present (for local/dev runs)
	// In Docker Compose, variables are injected by the runtime and this is harmless.
	_ = godotenv.Load()

	// 初始化数据库配置
	dbPath := "config.db"
	if len(os.Args) > 1 {
		dbPath = os.Args[1]
	}

	// 读取配置文件
	configFile, err := loadConfigFile()
	if err != nil {
		log.Fatalf("❌ 读取config.json失败: %v", err)
	}

	log.Printf("📋 初始化配置数据库: %s", dbPath)
	database, err := config.NewDatabase(dbPath)
	if err != nil {
		log.Fatalf("❌ 初始化数据库失败: %v", err)
	}
	defer database.Close()

	// 初始化加密服务
	log.Printf("🔐 初始化加密服务...")
	cryptoService, err := crypto.NewCryptoService("secrets/rsa_key")
	if err != nil {
		log.Fatalf("❌ 初始化加密服务失败: %v", err)
	}
	database.SetCryptoService(cryptoService)
	log.Printf("✅ 加密服务初始化成功")

	// 同步config.json到数据库
	if err := syncConfigToDatabase(database, configFile); err != nil {
		log.Printf("⚠️  同步config.json到数据库失败: %v", err)
	}

	// 加载内测码到数据库
	if err := loadBetaCodesToDatabase(database); err != nil {
		log.Printf("⚠️  加载内测码到数据库失败: %v", err)
	}

	// 获取系统配置
	useDefaultCoinsStr, _ := database.GetSystemConfig("use_default_coins")
	useDefaultCoins := useDefaultCoinsStr == "true"
	apiPortStr, _ := database.GetSystemConfig("api_server_port")

	// 设置JWT密钥（优先使用环境变量）
	jwtSecret := strings.TrimSpace(os.Getenv("JWT_SECRET"))
	if jwtSecret == "" {
		// 回退到数据库配置
		jwtSecret, _ = database.GetSystemConfig("jwt_secret")
		if jwtSecret == "" {
			jwtSecret = "your-jwt-secret-key-change-in-production-make-it-long-and-random"
			log.Printf("⚠️  使用默认JWT密钥，建议使用加密设置脚本生成安全密钥")
		} else {
			log.Printf("🔑 使用数据库中JWT密钥")
		}
	} else {
		log.Printf("🔑 使用环境变量JWT密钥")
	}
	auth.SetJWTSecret(jwtSecret)

	// 管理员模式下需要管理员密码，缺失则退出

	log.Printf("✓ 配置数据库初始化成功")
	fmt.Println()

	// 从数据库读取默认主流币种列表
	defaultCoinsJSON, _ := database.GetSystemConfig("default_coins")
	var defaultCoins []string

	if defaultCoinsJSON != "" {
		// 尝试从JSON解析
		if err := json.Unmarshal([]byte(defaultCoinsJSON), &defaultCoins); err != nil {
			log.Printf("⚠️  解析default_coins配置失败: %v，使用硬编码默认值", err)
			defaultCoins = []string{"BTCUSDT", "ETHUSDT", "SOLUSDT", "BNBUSDT", "HYPEUSDT", "SUIUSDT"}
		} else {
			log.Printf("✓ 从数据库加载默认币种列表（共%d个）: %v", len(defaultCoins), defaultCoins)
		}
	} else {
		// 如果数据库中没有配置，使用硬编码默认值
		defaultCoins = []string{"BTCUSDT", "ETHUSDT", "SOLUSDT", "BNBUSDT", "HYPEUSDT", "SUIUSDT"}
		log.Printf("⚠️  数据库中未配置default_coins，使用硬编码默认值")
	}

	pool.SetDefaultCoins(defaultCoins)
	// 设置是否使用默认主流币种
	pool.SetUseDefaultCoins(useDefaultCoins)
	if useDefaultCoins {
		log.Printf("✓ 已启用默认主流币种列表")
	}

	// 设置币种池API URL
	coinPoolAPIURL, _ := database.GetSystemConfig("coin_pool_api_url")
	if coinPoolAPIURL != "" {
		pool.SetCoinPoolAPI(coinPoolAPIURL)
		log.Printf("✓ 已配置AI500币种池API")
	}

	oiTopAPIURL, _ := database.GetSystemConfig("oi_top_api_url")
	if oiTopAPIURL != "" {
		pool.SetOITopAPI(oiTopAPIURL)
		log.Printf("✓ 已配置OI Top API")
	}

	// 创建TraderManager
	traderManager := manager.NewTraderManager()

	// 从数据库加载所有交易员到内存
	err = traderManager.LoadTradersFromDatabase(database)
	if err != nil {
		log.Fatalf("❌ 加载交易员失败: %v", err)
	}

	// 从数据库加载所有TG交易员到内存
	log.Printf("🔄 开始加载TG交易员...")
	err = traderManager.LoadTGTradersFromDatabase(database)
	if err != nil {
		log.Printf("⚠️ 加载TG交易员失败: %v", err)
		// TG交易员加载失败不应该导致程序退出，因为它们可能不存在
	} else {
		log.Printf("✅ TG交易员加载完成")
	}

	// 获取数据库中的所有交易员配置（用于显示，使用default用户）
	traders, err := database.GetTraders("default")
	if err != nil {
		log.Fatalf("❌ 获取交易员列表失败: %v", err)
	}

	// 显示加载的交易员信息
	fmt.Println()
	fmt.Println("🤖 数据库中的AI交易员配置:")
	if len(traders) == 0 {
		fmt.Println("  • 暂无配置的交易员，请通过Web界面创建")
	} else {
		for _, trader := range traders {
			status := "停止"
			if trader.IsRunning {
				status = "运行中"
			}
			fmt.Printf("  • %s (%s + %s) - 初始资金: %.0f USDT [%s]\n",
				trader.Name, strings.ToUpper(trader.AIModelID), strings.ToUpper(trader.ExchangeID),
				trader.InitialBalance, status)
		}
	}

	// 创建初始化上下文
	// TODO : 传入实际配置, 现在并未实际使用，未来所有模块初始化都将通过上下文传递配置
	// ctx := bootstrap.NewContext(&config.Config{})

	// // 执行所有初始化钩子
	// if err := bootstrap.Run(ctx); err != nil {
	// 	log.Fatalf("初始化失败: %v", err)
	// }

	fmt.Println()
	fmt.Println("🤖 AI全权决策模式:")
	fmt.Printf("  • AI将自主决定每笔交易的杠杆倍数（山寨币最高5倍，BTC/ETH最高5倍）\n")
	fmt.Println("  • AI将自主决定每笔交易的仓位大小")
	fmt.Println("  • AI将自主设置止损和止盈价格")
	fmt.Println("  • AI将基于市场数据、技术指标、账户状态做出全面分析")
	fmt.Println()
	fmt.Println("⚠️  风险提示: AI自动交易有风险，建议小额资金测试！")
	fmt.Println()
	fmt.Println("按 Ctrl+C 停止运行")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println()

	// 获取API服务器端口
	apiPort := 8080 // 默认端口
	if apiPortStr != "" {
		if port, err := strconv.Atoi(apiPortStr); err == nil {
			apiPort = port
		}
	}

	// 创建并启动API服务器
	apiServer := api.NewServer(traderManager, database, cryptoService, apiPort)
	go func() {
		if err := apiServer.Start(); err != nil {
			log.Printf("❌ API服务器错误: %v", err)
		}
	}()

	// 初始化并启动 Telegram Bot
	telegramConfig := config.LoadTelegramBotConfig()
	var telegramBot *telegram.TelegramBotManager
	if telegramConfig.Enabled {
		var err error
		telegramBot, err = telegram.NewTelegramBotManager(telegramConfig.BotToken, database, telegramConfig.Debug, telegramConfig.HyperliquidTestnet, traderManager)
		if err != nil {
			log.Printf("❌ Telegram Bot 初始化失败: %v", err)
		} else {
			go func() {
				log.Printf("🚀 Telegram Bot 启动中...")
				telegramBot.Start()
			}()
		}
	} else {
		log.Printf("ℹ️ Telegram Bot 已禁用")
	}

	// 启动流行情数据 - 默认使用所有交易员设置的币种 如果没有设置币种 则优先使用系统默认
	go market.NewWSMonitor(150).Start(database.GetCustomCoins())
	//go market.NewWSMonitor(150).Start([]string{}) //这里是一个使用方式 传入空的话 则使用market市场的所有币种
	// 设置优雅退出
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// 智能恢复：只恢复普通交易员，不恢复TG交易员（TG交易员需要用户手动启动）
	log.Println("🔄 检查并恢复运行中的普通交易员...")
	runningTraders, err := database.GetRunningTraders()
	if err != nil {
		log.Printf("⚠️  获取运行中交易员失败: %v", err)
	} else {
		if len(runningTraders) > 0 {
			restoredCount := 0
			log.Printf("📋 发现 %d 个运行中的交易员，开始恢复（跳过TG交易员）...", len(runningTraders))

			for _, traderRecord := range runningTraders {
				// 跳过TG交易员，它们需要用户手动启动
				if strings.HasPrefix(traderRecord.ID, "AI交易员-") {
					log.Printf("⏭️  跳过TG交易员: %s (%s) - 需要用户手动启动", traderRecord.Name, traderRecord.ID)
					continue
				}

				// 检查交易员是否已经在内存中
				if trader, err := traderManager.GetTrader(traderRecord.ID); err == nil {
					status := trader.GetStatus()
					if isRunning, ok := status["is_running"].(bool); ok && !isRunning {
						log.Printf("▶️  恢复交易员: %s (%s)", traderRecord.Name, traderRecord.ID)
						go func() {
							if err := trader.Run(); err != nil {
								log.Printf("❌ 交易员 %s 恢复失败: %v", trader.GetName(), err)
							}
						}()
						restoredCount++
					}
				} else {
					log.Printf("⚠️  交易员 %s (%s) 未在内存中，跳过恢复", traderRecord.Name, traderRecord.ID)
				}
			}

			log.Printf("✅ 交易员恢复完成，共恢复 %d 个普通交易员（TG交易员已跳过）", restoredCount)
		} else {
			log.Println("💡 没有发现运行中的交易员")
		}
	}

	// 恢复运行中的TG交易员
	if telegramBot != nil {
		log.Println("🔄 检查并恢复运行中的TG交易员...")
		tgRestoredCount := restoreTGTraders(database, telegramBot)
		log.Printf("✅ TG交易员恢复完成，共恢复 %d 个TG交易员", tgRestoredCount)
	}

	// 等待退出信号
	<-sigChan
	fmt.Println()
	fmt.Println()
	log.Println("📛 收到退出信号，正在停止所有trader...")
	traderManager.StopAll()

	fmt.Println()
	fmt.Println("👋 感谢使用AI交易系统！")
}

// restoreTGTraders 恢复运行中的TG交易员
func restoreTGTraders(database config.DatabaseInterface, telegramBot *telegram.TelegramBotManager) int {
	// 获取所有TG用户
	tgUserIDs, err := database.GetAllTGUsers()
	if err != nil {
		log.Printf("⚠️ 获取TG用户列表失败: %v", err)
		return 0
	}

	restoredCount := 0
	log.Printf("📋 发现 %d 个TG用户，检查运行中的TG交易员...", len(tgUserIDs))

	for _, tgUserID := range tgUserIDs {
		tgTraders, err := database.GetTgTraders(tgUserID)
		if err != nil {
			log.Printf("⚠️ 获取TG用户 %d 的交易员失败: %v", tgUserID, err)
			continue
		}

		for _, tgTrader := range tgTraders {
			if tgTrader.IsRunning {
				log.Printf("🔄 发现运行中的TG交易员: %s (%s)", tgTrader.Name, tgTrader.ID)

				// 尝试通过TelegramBotManager获取已加载的交易员实例（确保设置了TelegramBotManager）
				if traderObj, err := telegramBot.GetTelegramTraderManager().GetTgTrader(tgTrader.ID); err == nil {
					// 检查交易员是否真的在运行
					status := traderObj.GetStatus()
					if isRunning, ok := status["is_running"].(bool); ok && !isRunning {
						// 交易员存在但未运行，启动它
						log.Printf("▶️ 启动TG交易员: %s (%s)", tgTrader.Name, tgTrader.ID)
						go func() {
							if err := traderObj.Run(); err != nil {
								log.Printf("❌ TG交易员 %s 启动失败: %v", tgTrader.Name, err)
								// 更新数据库状态为停止
								if updateErr := database.UpdateTgTraderStatus(tgUserID, tgTrader.ID, false); updateErr != nil {
									log.Printf("更新TG交易员状态失败: %v", updateErr)
								}
							} else {
								log.Printf("✅ TG交易员 %s 启动成功", tgTrader.Name)
							}
						}()
						restoredCount++
					} else if isRunning {
						log.Printf("✅ TG交易员 %s 已在运行中", tgTrader.Name)
						restoredCount++
					}
				} else {
					// 交易员不在内存中，需要动态加载
					log.Printf("🔍 TG交易员 %s 不在内存中，尝试动态加载...", tgTrader.Name)
					// 类型断言：将DatabaseInterface转换为*config.Database
					dbConcrete, ok := database.(*config.Database)
					if !ok {
						log.Printf("❌ 数据库类型断言失败，无法动态加载TG交易员")
						continue
					}
					// 直接通过TelegramBotManager的traderMgr动态加载TG交易员
					if err := telegramBot.GetTraderManager().LoadTGTradersFromDatabase(dbConcrete); err != nil {
						log.Printf("❌ 动态加载TG交易员失败: %v", err)
						continue
					}

					// 再次尝试通过TelegramBotManager获取并启动（确保设置了TelegramBotManager）
					if traderObj, err := telegramBot.GetTelegramTraderManager().GetTgTrader(tgTrader.ID); err == nil {
						log.Printf("▶️ 启动动态加载的TG交易员: %s (%s)", tgTrader.Name, tgTrader.ID)
						go func() {
							if err := traderObj.Run(); err != nil {
								log.Printf("❌ TG交易员 %s 启动失败: %v", tgTrader.Name, err)
								// 更新数据库状态为停止
								if updateErr := database.UpdateTgTraderStatus(tgUserID, tgTrader.ID, false); updateErr != nil {
									log.Printf("更新TG交易员状态失败: %v", updateErr)
								}
							} else {
								log.Printf("✅ TG交易员 %s 动态加载并启动成功", tgTrader.Name)
							}
						}()
						restoredCount++
					} else {
						log.Printf("❌ 动态加载TG交易员 %s 后仍无法获取实例", tgTrader.Name)
						// 更新数据库状态为停止，避免不一致
						if updateErr := database.UpdateTgTraderStatus(tgUserID, tgTrader.ID, false); updateErr != nil {
							log.Printf("更新TG交易员状态失败: %v", updateErr)
						}
					}
				}
			}
		}
	}

	return restoredCount
}
