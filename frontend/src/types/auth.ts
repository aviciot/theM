export interface TheMUser {
  id: number;
  email: string;
  username: string;
  name: string;
  role: string;
  tenant_id?: string;
}

export interface TokenResponse {
  access_token: string;
  refresh_token?: string;
  token_type: string;
}

export interface AuthState {
  user: TheMUser | null;
  isAuthenticated: boolean;
  isLoading: boolean;
  error: string | null;
  login: (email: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
  fetchUser: () => Promise<boolean>;
  clearError: () => void;
}
