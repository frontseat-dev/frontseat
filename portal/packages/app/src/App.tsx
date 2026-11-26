import { createApp } from '@backstage/frontend-defaults';
import { dynamicFrontendFeaturesLoader } from '@backstage/frontend-dynamic-feature-loader';
import catalogPlugin from '@backstage/plugin-catalog/alpha';
import { navModule } from './modules/nav';
import { homeModule } from './modules/home';

export default createApp({
  // What a backend serves as dynamic plugins is loaded as the app starts:
  // Frontseat Enterprise's, where the backend is an enterprise one.
  features: [catalogPlugin, navModule, homeModule, dynamicFrontendFeaturesLoader()],
});
