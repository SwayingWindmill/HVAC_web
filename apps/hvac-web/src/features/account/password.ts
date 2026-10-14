import { PlatformApiError, createPlatformGatewayClient, type OwnPasswordRequest } from '@/api/generated/platformGateway.gen';

const client = createPlatformGatewayClient();

/** The rule a new password must meet, as the identity directory enforces it. */
export const PASSWORD_HINT = '至少 8 位，不能是常见密码，也不能和用户名相同';

const PASSWORD_MESSAGES: Readonly<Record<string, string>> = {
  PASSWORD_TOO_SHORT: '密码至少要 8 位',
  PASSWORD_TOO_LONG: '密码太长了',
  PASSWORD_TOO_COMMON: '这个密码太常见，容易被猜到，请换一个',
  PASSWORD_MATCHES_USERNAME: '密码不能和用户名相同',
  CURRENT_PASSWORD_INVALID: '当前密码不对。连续输错 5 次会锁定 15 分钟',
};

/** Why a password was refused, or undefined when the refusal is not about the password. */
export function passwordRefusal(error: unknown): string | undefined {
  return error instanceof PlatformApiError ? PASSWORD_MESSAGES[error.problem.code] : undefined;
}

export const changeOwnPassword = (csrfToken: string, body: OwnPasswordRequest) =>
  client.changeOwnPassword(body, { headers: { 'X-CSRF-Token': csrfToken } });
