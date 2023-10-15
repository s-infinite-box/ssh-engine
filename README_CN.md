# ssh-engine

通过编辑Yaml配置文件即可实现多个节点的shell命令的编排执行,只需要ssh服务开启即可,不需要安装任何agent
### Feature

1. 可以添加自定义变量, 在yaml中使用${[]}引用
  
2. 异步执行
  
3. 可编排: 其他命令的执行可以作为前置条件
  

### Temp example

[example.yaml](pkg/ssh-engine/EngineTemp.yaml)
