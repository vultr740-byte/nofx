// 系统状态
export interface SystemStatus {
  is_running: boolean
  start_time: string
  runtime_minutes: number
  call_count: number
  initial_balance: number
  scan_interval: string
  stop_until: string
  last_reset_time: string
  ai_provider: string
}

// 账户信息
export interface AccountInfo {
  total_equity: number
  available_balance: number
  total_pnl: number
  total_pnl_pct: number
  total_unrealized_pnl: number
  margin_used: number
  margin_used_pct: number
  position_count: number
  initial_balance: number
  daily_pnl: number
}

// 持仓信息
export interface Position {
  symbol: string
  side: string
  entry_price: number
  mark_price: number
  quantity: number
  leverage: number
  unrealized_pnl: number
  unrealized_pnl_pct: number
  liquidation_price: number
  margin_used: number
}

// 决策动作
export interface DecisionAction {
  action: string
  symbol: string
  quantity: number
  leverage: number
  price: number
  order_id: number
  timestamp: string
  success: boolean
  error: string
}

// 决策记录
export interface DecisionRecord {
  timestamp: string
  cycle_number: number
  input_prompt: string
  cot_trace: string
  decision_json: string
  account_state: {
    total_balance: number
    available_balance: number
    total_unrealized_profit: number
    position_count: number
    margin_used_pct: number
  }
  positions: Array<{
    symbol: string
    side: string
    position_amt: number
    entry_price: number
    mark_price: number
    unrealized_profit: number
    leverage: number
    liquidation_price: number
  }>
  candidate_coins: string[]
  decisions: DecisionAction[]
  execution_log: string[]
  success: boolean
  error_message: string
}

// 统计信息
export interface Statistics {
  total_cycles: number
  successful_cycles: number
  failed_cycles: number
  total_open_positions: number
  total_close_positions: number
}

// AI模型相关类型
export interface AIModel {
  id: string
  name: string
  provider: string
  enabled: boolean
  customApiUrl?: string
  customModelName?: string
  apiKey?: string // 仅前端使用，不传输到后端
}

export interface CreateModelConfigRequest {
  model_name: string
  provider_name: string
  api_key: string
  custom_api_url?: string
  custom_model_name?: string
  enabled: boolean
}

export interface UpdateModelConfigRequest {
  models: {
    [key: string]: {
      enabled: boolean
      api_key: string
      custom_api_url: string
      custom_model_name: string
    }
  }
}
