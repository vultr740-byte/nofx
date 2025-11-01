// 系统状态
export interface SystemStatus {
  trader_id?: string;
  trader_name?: string;
  ai_model?: string;
  is_running: boolean;
  start_time: string;
  runtime_minutes: number;
  call_count: number;
  initial_balance: number;
  scan_interval: string;
  stop_until: string;
  last_reset_time: string;
  ai_provider: string;
}

// 账户信息 - 合并两个版本的完整字段
export interface AccountInfo {
  total_equity: number;
  wallet_balance: number;
  unrealized_profit: number;
  available_balance: number;
  total_pnl: number;
  total_pnl_pct: number;
  total_unrealized_pnl: number;
  margin_used: number;
  margin_used_pct: number;
  position_count: number;
  initial_balance: number;
  daily_pnl: number;
}

// 持仓信息
export interface Position {
  symbol: string;
  side: string;
  entry_price: number;
  mark_price: number;
  quantity: number;
  leverage: number;
  unrealized_pnl: number;
  unrealized_pnl_pct: number;
  liquidation_price: number;
  margin_used: number;
}

// 决策动作 - 统一error字段为可选
export interface DecisionAction {
  action: string;
  symbol: string;
  quantity: number;
  leverage: number;
  price: number;
  order_id: number;
  timestamp: string;
  success: boolean;
  error?: string;
}

// 账户快照
export interface AccountSnapshot {
  total_balance: number;
  available_balance: number;
  total_unrealized_profit: number;
  position_count: number;
  margin_used_pct: number;
}

// 决策记录
export interface DecisionRecord {
  timestamp: string;
  cycle_number: number;
  input_prompt: string;
  cot_trace: string;
  decision_json: string;
  account_state: AccountSnapshot;
  positions: Array<{
    symbol: string;
    side: string;
    position_amt: number;
    entry_price: number;
    mark_price: number;
    unrealized_profit: number;
    leverage: number;
    liquidation_price: number;
  }>;
  candidate_coins: string[];
  decisions: DecisionAction[];
  execution_log: string[];
  success: boolean;
  error_message?: string;
}

// 统计信息
export interface Statistics {
  total_cycles: number;
  successful_cycles: number;
  failed_cycles: number;
  total_open_positions: number;
  total_close_positions: number;
}

// AI Trading相关类型
export interface TraderInfo {
  trader_id: string;
  trader_name: string;
  ai_model: string;
  ai_model_id?: string;
  exchange_id?: string;
  exchange_type?: string;
  is_running?: boolean;
  custom_prompt?: string;
  description?: string;
  enabled?: boolean;
  initial_balance?: number;
  scan_interval_minutes?: number;
  created_at?: string;
  updated_at?: string;
}

export interface AIModel {
  id: string;
  name: string;
  provider: string;
  enabled: boolean;
  api_key?: string;
  description?: string;
  user_id?: string;
  created_at?: string;
  updated_at?: string;
}

export interface Exchange {
  id: string;
  name: string;
  type: 'cex' | 'dex';
  exchange_type: string;
  enabled: boolean;
  api_key?: string;
  secret_key?: string;
  testnet?: boolean;
  // Hyperliquid 特定字段
  hyperliquid_wallet_addr?: string;
  // Aster DEX 特定字段
  aster_user?: string;
  aster_signer?: string;
  aster_private_key?: string;
  description?: string;
  user_id?: string;
  created_at?: string;
  updated_at?: string;
}

export interface CreateTraderRequest {
  name: string;
  ai_model_id: string;
  exchange_id: string;
  initial_balance?: number;
  custom_prompt?: string;
  override_base_prompt?: boolean;
  is_cross_margin?: boolean;
  scan_interval_minutes?: number;
  description?: string;
}

export interface UpdateModelConfigRequest {
  models: {
    [key: string]: {
      enabled: boolean;
      api_key: string;
    };
  };
}

export interface UpdateExchangeConfigRequest {
  exchanges: {
    [key: string]: {
      enabled: boolean;
      api_key: string;
      secret_key: string;
      testnet?: boolean;
      // Hyperliquid 特定字段
      hyperliquid_wallet_addr?: string;
      // Aster DEX 特定字段
      aster_user?: string;
      aster_signer?: string;
      aster_private_key?: string;
    };
  };
}

// 竞赛相关类型
export interface CompetitionTraderData {
  trader_id: string;
  trader_name: string;
  ai_model: string;
  total_equity: number;
  total_pnl: number;
  total_pnl_pct: number;
  position_count: number;
  margin_used_pct: number;
  is_running: boolean;
}

export interface CompetitionData {
  traders: CompetitionTraderData[];
  count: number;
}

// 新增：创建模型请求
export interface CreateModelRequest {
  name: string;
  provider: string;
  enabled?: boolean;
  api_key?: string;
  description?: string;
}

// 新增：创建交易所请求
export interface CreateExchangeRequest {
  name: string;
  type: string;
  enabled?: boolean;
  api_key?: string;
  secret_key?: string;
  testnet?: boolean;
  hyperliquid_wallet_addr?: string;
  aster_user?: string;
  aster_signer?: string;
  aster_private_key?: string;
  description?: string;
}

// 新增：更新模型请求
export interface UpdateModelRequest {
  name?: string;
  provider?: string;
  enabled?: boolean;
  api_key?: string;
  description?: string;
}

// 新增：更新交易所请求
export interface UpdateExchangeRequest {
  name?: string;
  type?: string;
  enabled?: boolean;
  api_key?: string;
  secret_key?: string;
  testnet?: boolean;
  hyperliquid_wallet_addr?: string;
  aster_user?: string;
  aster_signer?: string;
  aster_private_key?: string;
  description?: string;
}