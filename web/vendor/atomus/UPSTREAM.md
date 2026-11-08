# Atomus 来源

- Repository: https://github.com/StanVisionAgency/atomus
- Commit: 817ad1d80539523af5b92145f5e5b580fe642131
- 上游 React package.json version: 4.1.0（官网品牌为 Atomus 4.0）
- License: MIT，见 LICENSE 和 NOTICE。

仅保留 css/atomus.css、react/src 运行组件及工具。没有引入 Storybook、Figma 模板、测试、构建脚本或上游 node_modules。
组件源码未修改，通过 Vite alias 直接引用 TSX，不需要事先构建 Atomus 包。
更新需显式选择新提交并复核组件 API，不跟随 main 自动更新。
