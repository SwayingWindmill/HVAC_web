import { defineConfig } from 'vite';
import { createHvacWebConfig } from './vite.shared.config';

export default defineConfig(createHvacWebConfig('real'));
