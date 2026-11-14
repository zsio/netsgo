import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { QueryClientProvider } from '@tanstack/react-query';
import { RouterProvider } from '@tanstack/react-router';
import { queryClient } from '@/lib/query-client';
import { router } from '@/lib/router';
import { Toaster } from 'react-hot-toast';
import '@/i18n';
import './index.css';

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
      <Toaster
        position="bottom-right"
        containerStyle={{
          bottom: 'calc(1rem + var(--safe-area-bottom, 0px))',
          right: 'calc(1rem + var(--safe-area-right, 0px))',
        }}
        toastOptions={{
          className: 'text-sm font-medium shadow-lg backdrop-blur-md',
          style: {
            background: 'var(--popover)',
            color: 'var(--popover-foreground)',
            border: '1px solid var(--border)',
            borderRadius: 'var(--radius-lg)',
          },
          success: {
            iconTheme: {
              primary: 'var(--status-online)',
              secondary: 'var(--popover)',
            },
          },
          error: {
            iconTheme: {
              primary: 'var(--status-danger)',
              secondary: 'var(--popover)',
            },
          },
        }}
      />
    </QueryClientProvider>
  </StrictMode>,
);
