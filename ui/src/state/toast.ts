// Small toast notification store. Pages/components call pushToast(); App
// renders the queue via components/Toasts.tsx.
import { create } from 'zustand';

export interface Toast {
  id: number;
  kind: 'info' | 'error' | 'success';
  message: string;
}

interface ToastState {
  toasts: Toast[];
  push: (kind: Toast['kind'], message: string) => void;
  dismiss: (id: number) => void;
}

let nextId = 1;

export const useToasts = create<ToastState>((set) => ({
  toasts: [],
  push: (kind, message) => {
    const id = nextId++;
    set((state) => ({ toasts: [...state.toasts, { id, kind, message }] }));
    setTimeout(() => {
      set((state) => ({ toasts: state.toasts.filter((t) => t.id !== id) }));
    }, 5000);
  },
  dismiss: (id) => set((state) => ({ toasts: state.toasts.filter((t) => t.id !== id) })),
}));

export function pushToast(kind: Toast['kind'], message: string) {
  useToasts.getState().push(kind, message);
}
