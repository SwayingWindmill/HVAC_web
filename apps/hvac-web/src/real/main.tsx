import React from 'react';
import ReactDOM from 'react-dom/client';
import RealApp, { RealConfigurationBlocked } from './RealApp';
import { validateRealRuntimeConfig } from './runtime-config';
import '@/global.css';

const runtimeConfig = validateRealRuntimeConfig();

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    {runtimeConfig.ok ? (
      <RealApp config={runtimeConfig.config} />
    ) : (
      <RealConfigurationBlocked failures={runtimeConfig.failures} />
    )}
  </React.StrictMode>,
);
