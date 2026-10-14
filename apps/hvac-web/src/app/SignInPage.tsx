import { useState, type ReactNode } from 'react';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card';
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import { Spinner } from '@/components/ui/spinner';

// The identity service owns the login flow and checks the password; this page is only its form.
// Both forms post straight to the identity service on the same origin (docs/specs/sign-in-page.md).
const IDENTITY_PATH = '/identity';
const USERNAME_KEY = 'hvac.sign-in.username';

const OUTCOME_MESSAGES: Record<string, string> = {
  invalid_credentials: '用户名或密码不对，请重新输入。',
  totp_invalid: '验证码不对或已经用过，请输入认证器上新的验证码。',
  mfa_enrollment_required: '这个账号需要先开通两步验证，请联系管理员。',
  unavailable: '登录服务暂时不可用，请稍后再试。',
};

export type SignInRequest =
  | { kind: 'form'; challenge: string; step: 'password' | 'totp'; message?: string }
  | { kind: 'expired' }
  | { kind: 'start' };

export function readSignInRequest(search: string): SignInRequest {
  const query = new URLSearchParams(search);
  const challenge = query.get('login_challenge');
  const error = query.get('error');
  if (challenge) {
    return {
      kind: 'form',
      challenge,
      step: query.get('step') === 'totp' ? 'totp' : 'password',
      message: error ? OUTCOME_MESSAGES[error] ?? OUTCOME_MESSAGES.unavailable : undefined,
    };
  }
  return error === 'expired' ? { kind: 'expired' } : { kind: 'start' };
}

function rememberedUsername(): string {
  try {
    return window.sessionStorage.getItem(USERNAME_KEY) ?? '';
  } catch {
    return '';
  }
}

function rememberUsername(username: string) {
  try {
    window.sessionStorage.setItem(USERNAME_KEY, username);
  } catch {
    // Without storage the field simply starts empty after a failed attempt.
  }
}

export function SignInLayout({ children }: { children: ReactNode }) {
  return (
    <div className="flex min-h-svh flex-col items-center justify-center gap-6 bg-muted p-10">
      <div className="flex w-full max-w-sm flex-col gap-6">
        <div className="flex items-center gap-2 self-center font-medium">
          <img src="/quanlaihe-mark.svg" alt="" className="size-7" />
          泉来禾智慧能源
        </div>
        {children}
      </div>
    </div>
  );
}

export function SignInNotice({ title, description, action, onAction }: { title: string; description: string; action: string; onAction: () => void }) {
  return (
    <SignInLayout>
      <Card data-testid="sign-in-notice">
        <CardHeader className="text-center">
          <CardTitle className="text-xl">{title}</CardTitle>
          <CardDescription>{description}</CardDescription>
        </CardHeader>
        <CardContent>
          <Button type="button" className="w-full" onClick={onAction}>{action}</Button>
        </CardContent>
      </Card>
    </SignInLayout>
  );
}

export function SignInForm({ request }: { request: Extract<SignInRequest, { kind: 'form' }> }) {
  const [submitting, setSubmitting] = useState(false);
  const totp = request.step === 'totp';
  return (
    <SignInLayout>
      <Card>
        <CardHeader className="text-center">
          <CardTitle className="text-xl">{totp ? '安全确认' : '登录'}</CardTitle>
          <CardDescription>{totp ? '请输入认证器上的 6 位验证码' : '使用平台账号登录'}</CardDescription>
        </CardHeader>
        <CardContent>
          <form
            method="post"
            action={`${IDENTITY_PATH}${totp ? '/mfa/totp' : '/login'}`}
            autoComplete={totp ? 'off' : 'on'}
            onSubmit={(event) => {
              const username = new FormData(event.currentTarget).get('username');
              if (typeof username === 'string') rememberUsername(username);
              setSubmitting(true);
            }}
          >
            <input type="hidden" name="challenge" value={request.challenge} />
            <FieldGroup>
              {request.message ? (
                <Alert variant="destructive">
                  <AlertDescription>{request.message}</AlertDescription>
                </Alert>
              ) : null}
              {totp ? (
                <Field>
                  <FieldLabel htmlFor="code">一次性验证码</FieldLabel>
                  <Input id="code" name="code" inputMode="numeric" autoComplete="one-time-code" pattern="[0-9]{6}" minLength={6} maxLength={6} required autoFocus />
                </Field>
              ) : (
                <>
                  <Field>
                    <FieldLabel htmlFor="username">用户名</FieldLabel>
                    <Input id="username" name="username" autoComplete="username" defaultValue={rememberedUsername()} required autoFocus />
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="password">密码</FieldLabel>
                    <Input id="password" name="password" type="password" autoComplete="current-password" required />
                  </Field>
                </>
              )}
              <Field>
                <Button type="submit" disabled={submitting}>
                  {submitting ? <Spinner /> : null}
                  {totp ? '确认' : '登录'}
                </Button>
              </Field>
            </FieldGroup>
          </form>
        </CardContent>
      </Card>
    </SignInLayout>
  );
}
