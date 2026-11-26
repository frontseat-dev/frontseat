import { createBackend } from '@backstage/backend-defaults';
import { helloPlugin } from '@hello/backend';

const backend = createBackend();
backend.add(helloPlugin);
backend.start();
