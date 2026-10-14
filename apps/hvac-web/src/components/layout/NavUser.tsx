import { useState } from 'react';
import { ChevronDown, KeyRound, LogOut, ShieldCheck } from 'lucide-react';
import { Avatar, AvatarFallback } from '@/components/ui/avatar';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { ChangePasswordDialog } from '@/features/account/ChangePasswordDialog';

interface NavUserProps {
  readonly name: string;
  readonly role: string;
  readonly csrfToken: string;
  readonly onNavigate: (target: string) => void;
  readonly onLogout: () => void;
}

/** The signed-in user at the right of the top bar: who they are, and the way out. */
export function NavUser({ name, role, csrfToken, onNavigate, onLogout }: NavUserProps) {
  const [changingPassword, setChangingPassword] = useState(false);
  return (
    <>
    <DropdownMenu modal={false}>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          aria-label={`${name}，用户菜单`}
          className="flex h-8 items-center gap-2 rounded-lg pr-1.5 pl-1 text-sm text-secondary-foreground transition-colors duration-100 outline-none hover:bg-accent focus-visible:ring-3 focus-visible:ring-ring-soft data-[state=open]:bg-accent"
        >
          <Avatar className="size-7">
            <AvatarFallback className="bg-primary-soft text-xs font-semibold text-primary">{name.slice(0, 1)}</AvatarFallback>
          </Avatar>
          <span className="max-w-32 truncate">{name}</span>
          <ChevronDown className="size-3.5 text-subtle-foreground" aria-hidden="true" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent className="min-w-56" align="end" sideOffset={6}>
        <DropdownMenuLabel className="flex flex-col gap-0.5 font-normal">
          <span className="text-sm font-semibold text-foreground">{name}</span>
          <span className="text-xs text-muted-foreground">{role}</span>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuGroup>
          <DropdownMenuItem onSelect={() => onNavigate('/system')}>
            <ShieldCheck />
            系统管理
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => setChangingPassword(true)}>
            <KeyRound />
            修改密码
          </DropdownMenuItem>
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        <DropdownMenuItem variant="destructive" onSelect={onLogout}>
          <LogOut />
          退出登录
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
    <ChangePasswordDialog open={changingPassword} onOpenChange={setChangingPassword} csrfToken={csrfToken} />
    </>
  );
}
