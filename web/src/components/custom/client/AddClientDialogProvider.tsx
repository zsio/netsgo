import { useCallback, useMemo, useState, type ReactNode } from 'react';

import { AddClientDialog } from './AddClientDialog';
import { AddClientDialogContext } from './add-client-dialog-context';

export function AddClientDialogProvider({ children }: { children: ReactNode }) {
  const [open, setOpen] = useState(false);
  const openAddClientDialog = useCallback(() => setOpen(true), []);
  const value = useMemo(() => ({ openAddClientDialog }), [openAddClientDialog]);

  return (
    <AddClientDialogContext.Provider value={value}>
      {children}
      <AddClientDialog open={open} onOpenChange={setOpen} />
    </AddClientDialogContext.Provider>
  );
}
