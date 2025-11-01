import { UserState } from '../types/api';

export class StateManager {
  private userStates: Map<number, UserState> = new Map();
  private readonly STATE_TIMEOUT = 30 * 60 * 1000; // 30分钟超时

  // 获取用户状态
  getState(userId: number): UserState | undefined {
    const state = this.userStates.get(userId);
    if (!state) return undefined;

    // 检查状态是否过期
    if (Date.now() - state.createdAt > this.STATE_TIMEOUT) {
      this.clearState(userId);
      return undefined;
    }

    return state;
  }

  // 设置用户状态
  setState(userId: number, action: string, data: Record<string, any> = {}): void {
    this.userStates.set(userId, {
      userId,
      action: action as any,
      data,
      createdAt: Date.now(),
    });
  }

  // 更新用户状态数据
  updateState(userId: number, data: Record<string, any>): void {
    const state = this.getState(userId);
    if (state) {
      state.data = { ...state.data, ...data };
      this.userStates.set(userId, state);
    }
  }

  // 清除用户状态
  clearState(userId: number): void {
    this.userStates.delete(userId);
  }

  // 检查用户是否在某个流程中
  isInFlow(userId: number, action: string): boolean {
    const state = this.getState(userId);
    return state?.action === action;
  }

  // 清理过期状态
  cleanupExpiredStates(): void {
    const now = Date.now();
    for (const [userId, state] of this.userStates.entries()) {
      if (now - state.createdAt > this.STATE_TIMEOUT) {
        this.userStates.delete(userId);
      }
    }
  }

  // 获取当前活跃用户数量
  getActiveUserCount(): number {
    this.cleanupExpiredStates();
    return this.userStates.size;
  }

  // 获取所有用户状态（用于调试）
  getAllStates(): Map<number, UserState> {
    this.cleanupExpiredStates();
    return new Map(this.userStates);
  }
}

// 全局状态管理器实例
export const stateManager = new StateManager();