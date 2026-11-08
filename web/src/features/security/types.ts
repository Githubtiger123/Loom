export type DeviceSession = {
  familyId: string;
  current: boolean;
  createdAt: string;
  lastRefreshedAt: string;
  expiresAt: string;
};
export type SessionPage = { items: DeviceSession[]; nextAfter?: string };
