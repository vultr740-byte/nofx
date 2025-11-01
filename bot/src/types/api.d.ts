export interface ApiResponse<T = any> {
    success: boolean;
    data?: T;
    error?: string;
}
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
export interface AIModel {
    id: string;
    name: string;
    provider: string;
    enabled: boolean;
    description?: string;
}
export interface Exchange {
    id: string;
    name: string;
    exchange_type: string;
    enabled: boolean;
    testnet?: boolean;
    description?: string;
}
export interface UserState {
    userId: number;
    action?: 'create_trader' | 'select_ai_model' | 'select_exchange' | 'confirm_create';
    data?: Record<string, any>;
    createdAt: number;
}
export interface BotMenuOption {
    text: string;
    callback_data: string;
}
export interface BotError {
    message: string;
    code?: string;
    details?: any;
}
//# sourceMappingURL=api.d.ts.map