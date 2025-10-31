export interface SystemConfig {
  admin_mode: boolean;
}

let configPromise: Promise<SystemConfig> | null = null;
let cachedConfig: SystemConfig | null = null;

const isDev = window.location.origin.includes('localhost') || 
              window.location.origin.includes('127.0.0.1');

const API_BASE = isDev
  ? '/api'
  : 'https://nofx-2vs8.onrender.com/api';

export function getSystemConfig(): Promise<SystemConfig> {
  if (cachedConfig) {
    return Promise.resolve(cachedConfig);
  }
  if (configPromise) {
    return configPromise;
  }
  configPromise = fetch(`${API_BASE}/config`)
    .then((res) => res.json())
    .then((data: SystemConfig) => {
      cachedConfig = data;
      return data;
    })
    .finally(() => {
      // Keep cachedConfig for reuse; allow re-fetch via explicit invalidation if added later
    });
  return configPromise;
}


