// API Response Types
export interface ApiResponse<T = any> {
  success: boolean;
  data?: T;
  error?: string;
}

// Trader Types
export interface Trader {
  trader_id: string;
  trader_name: string;
  display_name?: string;
  ai_model: string;
  exchange_type?: string;
  total_equity: number;
  total_pnl: number;
  total_pnl_pct: number;
  position_count: number;
  margin_used_pct: number;
  is_running: boolean;
  created_at: string;
}

export interface CreateTraderRequest {
  name: string;
  ai_model_id: string;
  exchange_id: string;
  initial_balance?: number;
  scan_interval_minutes?: number;
  is_cross_margin?: boolean;
}

export interface TraderStatus {
  is_running: boolean;
  last_decision?: string;
  last_update?: string;
}

// AI Model Types
export interface AIModel {
  id: string;
  name: string;
  provider: string;
  enabled: boolean;
  description?: string;
}

export interface CreateAIModelRequest {
  name: string;
  provider: string;
  api_key?: string;
  description?: string;
  enabled?: boolean;
}

// Exchange Types
export interface Exchange {
  id: string;
  name: string;
  exchange_type: string;
  enabled: boolean;
  testnet?: boolean;
  description?: string;
}

// User State for Bot Conversations
export interface UserState {
  userId: number;
  action?:
    | 'enter_model_name'
    | 'select_model_provider'
    | 'enter_api_key'
    | 'enter_description'
    | 'create_trader'
    | 'enter_trader_name'
    | 'select_ai_model'
    | 'select_exchange'
    | 'enter_initial_balance'
    | 'confirm_create';
  data?: Record<string, any>;
  createdAt: number;
}

// Trader Creation Steps
export interface TraderCreationData {
  name?: string;
  ai_model_id?: string;
  exchange_id?: string;
  initial_balance?: number;
  scan_interval_minutes?: number;
  is_cross_margin?: boolean;
}

// Bot Menu Options
export interface BotMenuOption {
  text: string;
  callback_data: string;
}

// Error Types
export interface BotError {
  message: string;
  code?: string;
  details?: any;
}