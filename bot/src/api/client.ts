import { ApiResponse, Trader, CreateTraderRequest, AIModel, Exchange, CreateAIModelRequest } from '../types/api';
import * as crypto from 'node:crypto';

export class ApiClient {
  private baseUrl: string;
  private botToken: string;
  private apiSecret: string;

  constructor() {
    this.baseUrl = process.env.GO_API_BASE_URL || 'http://localhost:8080';
    this.botToken = process.env.BOT_API_TOKEN || '';
    this.apiSecret = process.env.BOT_API_SECRET || '';
  }

  private generateSignature(timestamp: number, body: string): string {
    const data = `${timestamp}${body}`;
    return crypto.createHmac('sha256', this.apiSecret).update(data).digest('hex');
  }

  private async makeRequest<T>(
    endpoint: string,
    options: { method?: string; body?: any; headers?: Record<string, string>; telegramUserId?: number } = {}
  ): Promise<ApiResponse<T>> {
    const timestamp = Math.floor(Date.now() / 1000);
    const body = options.body ? JSON.stringify(options.body) : '';
    const signature = this.generateSignature(timestamp, body);

    const headers = {
      'Content-Type': 'application/json',
      'X-Bot-Token': this.botToken,
      'X-Bot-Timestamp': timestamp.toString(),
      'X-Bot-Signature': signature,
      ...(options.telegramUserId && { 'X-Telegram-User-ID': options.telegramUserId.toString() }),
      ...options.headers,
    };

    try {
      const response = await fetch(`${this.baseUrl}${endpoint}`, {
        ...options,
        headers,
      });

      if (!response.ok) {
        throw new Error(`API request failed: ${response.status} ${response.statusText}`);
      }

      const data = await response.json();
      return { success: true, data };
    } catch (error) {
      console.error('API Error:', error);
      return {
        success: false,
        error: error instanceof Error ? error.message : 'Unknown error',
      };
    }
  }

  // Trader APIs
  async getTraders(telegramUserId: number): Promise<ApiResponse<Trader[]>> {
    return this.makeRequest<Trader[]>('/api/bot/traders', { telegramUserId });
  }

  async createTrader(userId: number, traderData: CreateTraderRequest): Promise<ApiResponse<Trader>> {
    return this.makeRequest<Trader>('/api/bot/traders', {
      method: 'POST',
      body: { ...traderData, telegram_user_id: userId },
      telegramUserId: userId,
    });
  }

  async startTrader(traderId: string, telegramUserId: number): Promise<ApiResponse<any>> {
    return this.makeRequest(`/api/bot/traders/${traderId}/start`, {
      method: 'POST',
      telegramUserId,
    });
  }

  async stopTrader(traderId: string, telegramUserId: number): Promise<ApiResponse<any>> {
    return this.makeRequest(`/api/bot/traders/${traderId}/stop`, {
      method: 'POST',
      telegramUserId,
    });
  }

  async getTraderStatus(traderId: string, telegramUserId: number): Promise<ApiResponse<any>> {
    return this.makeRequest(`/api/bot/traders/${traderId}/status`, { telegramUserId });
  }

  // AI Model APIs
  async getAIModels(telegramUserId: number): Promise<ApiResponse<AIModel[]>> {
    return this.makeRequest<AIModel[]>('/api/bot/ai-models', { telegramUserId });
  }

  async createAIModel(userId: number, modelData: CreateAIModelRequest): Promise<ApiResponse<AIModel>> {
    return this.makeRequest<AIModel>('/api/bot/ai-models', {
      method: 'POST',
      body: { ...modelData, telegram_user_id: userId },
      telegramUserId: userId,
    });
  }

  // Exchange APIs
  async getExchanges(telegramUserId: number): Promise<ApiResponse<Exchange[]>> {
    return this.makeRequest<Exchange[]>('/api/bot/exchanges', { telegramUserId });
  }

  // Health Check
  async healthCheck(): Promise<ApiResponse<{ status: string }>> {
    return this.makeRequest<{ status: string }>('/api/bot/health');
  }
}