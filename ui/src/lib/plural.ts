// "1 commit", "2 commits": the count with its noun, for labels and toasts.
export function plural(n: number, noun: string): string {
  return `${n} ${noun}${n === 1 ? '' : 's'}`;
}
