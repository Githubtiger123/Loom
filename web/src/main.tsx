import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { QueryClientProvider } from '@tanstack/react-query';
import { RouterProvider } from 'react-router-dom';
import '@fontsource/inter/400.css';
import '@fontsource/inter/500.css';
import '@fontsource/inter/600.css';
import '@fontsource/inter/700.css';
import '@fontsource/roboto-mono/400.css';
import '../vendor/atomus/atomus.css';
import '../vendor/atomus/react/styles.css';
import './styles/app.css';
import { queryClient } from './lib/query';
import { router } from './app/router';

const saved = localStorage.getItem('loom.theme');
document.documentElement.dataset.theme = saved === 'dark' ? 'dark' : 'light';
createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
);
