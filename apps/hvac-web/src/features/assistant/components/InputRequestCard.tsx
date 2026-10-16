import { Button } from '@/components/ui/button';
import type { AssistantPart } from '../thread-model';

type InputRequest = Extract<AssistantPart, { type: 'data-input-request' }>['data'];

/** A question the assistant needs answered before it can go on. Choices answer in one click; free text goes through the composer. */
export function InputRequestCard({ request, onAnswer, open }: {
  readonly request: InputRequest;
  readonly onAnswer: (value: string) => void;
  /** Only the latest unanswered question takes answers. */
  readonly open: boolean;
}) {
  return (
    <section aria-label="需要补充信息" className="flex flex-col gap-2.5 rounded-lg border bg-card p-3.5" data-testid="assistant-input-request">
      <span className="text-xs font-medium text-muted-foreground">需要补充信息</span>
      <p className="m-0 text-sm leading-relaxed">{request.prompt}</p>
      {request.response.kind === 'SINGLE_SELECT' ? (
        <div className="flex flex-wrap gap-2">
          {request.response.choices.map((choice) => (
            <Button key={choice.value} variant="outline" size="sm" disabled={!open} onClick={() => onAnswer(choice.value)}>
              {choice.label}
            </Button>
          ))}
        </div>
      ) : open ? (
        <span className="text-xs text-muted-foreground">在下方输入框里回答</span>
      ) : null}
    </section>
  );
}
