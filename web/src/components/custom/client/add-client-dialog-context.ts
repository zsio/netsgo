import { createContext, useContext } from 'react';

export interface AddClientDialogContextValue {
  openAddClientDialog: () => void;
}

export const AddClientDialogContext = createContext<AddClientDialogContextValue | null>(null);

export function useAddClientDialog() {
  const context = useContext(AddClientDialogContext);
  if (!context) {
    throw new Error('useAddClientDialog must be used within AddClientDialogProvider');
  }
  return context;
}
