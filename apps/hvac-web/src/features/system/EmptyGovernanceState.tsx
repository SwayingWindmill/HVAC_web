export function EmptyGovernanceState({ description }: { readonly description: string }) {
  return (
    <div className="grid min-h-32 place-items-center rounded-md border border-dashed p-6 text-center text-sm text-muted-foreground">
      {description}
    </div>
  );
}
