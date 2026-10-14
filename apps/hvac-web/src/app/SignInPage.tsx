import { useState, type ReactNode } from 'react';
import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field';
import { Input } from '@/components/ui/input';
import { Spinner } from '@/components/ui/spinner';
import { SignInIllustration } from './SignInIllustration';

// The identity service owns the login flow and checks the password; this page is only its form.
// Both forms post straight to the identity service on the same origin (docs/specs/sign-in-page.md).
const IDENTITY_PATH = '/identity';
const USERNAME_KEY = 'hvac.sign-in.username';
// 「记住用户名」keeps the name on this computer; without it the name lives only in this tab.
const REMEMBERED_USERNAME_KEY = 'hvac.sign-in.remembered-username';

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

function storedUsername(storage: () => Storage, key: string): string {
  try {
    return storage().getItem(key) ?? '';
  } catch {
    return '';
  }
}

function rememberUsername(username: string, onThisComputer: boolean) {
  try {
    window.sessionStorage.setItem(USERNAME_KEY, username);
    if (onThisComputer) window.localStorage.setItem(REMEMBERED_USERNAME_KEY, username);
    else window.localStorage.removeItem(REMEMBERED_USERNAME_KEY);
  } catch {
    // Without storage the field simply starts empty next time.
  }
}

// shadcn login-02 turned around: the brand illustration fills the left, the form sits in a column on the
// right (docs/specs/sign-in-page.md).
export function SignInLayout({ children }: { children: ReactNode }) {
  return (
    <div className="grid min-h-svh grid-cols-[1fr_560px]">
      <BrandPanel />
      <div className="flex flex-col bg-card px-16 py-10 shadow-[-1px_0_0_var(--border)]">
        <div className="flex flex-1 items-center">
          <div className="w-full max-w-[400px]">{children}</div>
        </div>
        <p className="text-xs text-muted-foreground">© 2026 泉来禾智慧能源</p>
      </div>
    </div>
  );
}

// The illustration's palette: the design system's indigo, teal and amber, with darker grounds in the dark theme.
const ILLUSTRATION_COLORS = [
  '[--ill-blob:#e6ebfd] [--ill-pale:#dde3fa] [--ill-soft:#93a6f4] [--ill-primary:#3f5bd9] [--ill-ink:#1f2a5e] [--ill-card:#ffffff]',
  '[--ill-green:#0e9384] [--ill-amber:#f5b52e] [--ill-amber-deep:#dc9a12] [--ill-skin:#f2c3a0]',
  'dark:[--ill-blob:#1a1d2e] dark:[--ill-pale:#262a40] dark:[--ill-soft:#5b70d6] dark:[--ill-primary:#7b93ff] dark:[--ill-ink:#c9d2ff] dark:[--ill-card:#2a2e45] dark:[--ill-green:#16a394]',
].join(' ');

function BrandPanel() {
  return (
    <div className={`relative flex flex-col justify-between overflow-hidden bg-muted p-12 ${ILLUSTRATION_COLORS}`}>
      <div className="flex items-center gap-3 text-lg font-semibold">
        <img src="/quanlaihe-mark.svg" alt="" className="size-9" />
        泉来禾智慧能源
      </div>
      <div className="flex flex-1 items-center justify-center py-6">
        <SignInIllustration className="w-full max-w-[760px]" />
      </div>
      <h2 className="text-4xl font-semibold tracking-tight">让冷站省下每一度电</h2>
    </div>
  );
}

export function SignInNotice({ title, description, action, onAction }: { title: string; description: string; action: string; onAction: () => void }) {
  return (
    <SignInLayout>
      <div data-testid="sign-in-notice" className="flex flex-col gap-6">
        <SignInHeading title={title} description={description} />
        <Button type="button" className="w-full" onClick={onAction}>{action}</Button>
      </div>
    </SignInLayout>
  );
}

const INPUT = 'h-11 px-3.5 text-base';

function SignInHeading({ title, description }: { title: string; description: string }) {
  return (
    <div className="flex flex-col gap-2">
      <h1 className="text-4xl font-bold tracking-tight">{title}</h1>
      <p className="text-base text-muted-foreground">{description}</p>
    </div>
  );
}

export function SignInForm({ request }: { request: Extract<SignInRequest, { kind: 'form' }> }) {
  const [submitting, setSubmitting] = useState(false);
  const [rememberedName] = useState(() => storedUsername(() => window.localStorage, REMEMBERED_USERNAME_KEY));
  const [remember, setRemember] = useState(rememberedName !== '');
  const totp = request.step === 'totp';
  return (
    <SignInLayout>
      <form
        method="post"
        action={`${IDENTITY_PATH}${totp ? '/mfa/totp' : '/login'}`}
        autoComplete={totp ? 'off' : 'on'}
        onSubmit={(event) => {
          const username = new FormData(event.currentTarget).get('username');
          if (typeof username === 'string') rememberUsername(username, remember);
          setSubmitting(true);
        }}
      >
        <input type="hidden" name="challenge" value={request.challenge} />
        <FieldGroup>
          <SignInHeading title={totp ? '安全确认' : '欢迎回来'} description={totp ? '请输入认证器上的 6 位验证码' : '使用平台账号登录'} />
          {request.message ? (
            <Alert variant="destructive">
              <AlertDescription>{request.message}</AlertDescription>
            </Alert>
          ) : null}
          {totp ? (
            <Field>
              <FieldLabel htmlFor="code">一次性验证码</FieldLabel>
              <Input id="code" name="code" className={INPUT} placeholder="6 位数字" inputMode="numeric" autoComplete="one-time-code" pattern="[0-9]{6}" minLength={6} maxLength={6} required autoFocus />
            </Field>
          ) : (
            <>
              <Field>
                <FieldLabel htmlFor="username">用户名</FieldLabel>
                <Input id="username" name="username" className={INPUT} placeholder="请输入用户名" autoComplete="username" defaultValue={storedUsername(() => window.sessionStorage, USERNAME_KEY) || rememberedName} required autoFocus />
              </Field>
              <Field>
                <FieldLabel htmlFor="password">密码</FieldLabel>
                <Input id="password" name="password" type="password" className={INPUT} placeholder="请输入密码" autoComplete="current-password" required />
              </Field>
              <Field orientation="horizontal">
                <Checkbox id="remember" checked={remember} onCheckedChange={(checked) => setRemember(checked === true)} />
                <FieldLabel htmlFor="remember" className="font-normal">记住用户名</FieldLabel>
              </Field>
            </>
          )}
          <Field>
            <Button type="submit" size="lg" className="h-11 text-base" disabled={submitting}>
              {submitting ? <Spinner /> : null}
              {totp ? '确认' : '登录'}
            </Button>
          </Field>
        </FieldGroup>
      </form>
    </SignInLayout>
  );
}
