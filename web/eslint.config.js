import js from '@eslint/js'
import configPrettier from 'eslint-config-prettier'
import pluginVue from 'eslint-plugin-vue'
import globals from 'globals'
import tseslint from 'typescript-eslint'

export default tseslint.config(
  { ignores: ['dist/**', 'node_modules/**', 'coverage/**'] },

  js.configs.recommended,
  tseslint.configs.recommended,
  pluginVue.configs['flat/recommended'],

  {
    languageOptions: {
      globals: { ...globals.browser },
    },
  },

  {
    // .vue 里的 <script lang="ts">：外层是 vue-eslint-parser，内层交给 typescript-eslint
    files: ['**/*.vue'],
    languageOptions: {
      parserOptions: {
        parser: tseslint.parser,
        extraFileExtensions: ['.vue'],
      },
    },
  },

  // 必须放最后：关掉所有与 Prettier 冲突的格式类规则，格式统一交给 Prettier
  configPrettier,
)
