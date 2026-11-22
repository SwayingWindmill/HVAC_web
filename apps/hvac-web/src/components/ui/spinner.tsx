import { LoaderCircle } from 'lucide-react';
import { cn } from 'cn';

function Spinner({ className, ...props }: React.ComponentProps<'svg'>) {
  return <LoaderCircle data-slot="spinner" role="status" aria-label="正在加载" className={cn('size-4 motion-safe:animate-spin', className)} {...props} />;
}

export { Spinner };
