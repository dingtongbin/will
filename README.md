# will

**写法像动态语言，行为像系统语言。**

will 是一门面向用户态系统、网络服务、数据工具与实时程序的通用编程语言。它把表达式式的可读语法、编译期所有权检查、可预测的内存管理、结构化并发和直接的 C ABI 互操作放在同一门语言里。

> will 目前处于早期开发阶段。它已经能编译并运行一批真实程序，但还不适合用来交付生产系统。

## 为什么做 will

### 写代码时看不到生命周期

源文件里没有指针、引用和生命周期标注。一个值什么时候移动、什么时候借用、什么时候释放，由编译器推导；写代码的人只需要描述值本身。

```will
struct Account {
    name: str
    balance: i64
}

func Deposit(mut account: Account, amount: i64) {
    account.balance += amount
}
```

### 内存行为可预测

will 使用值语义与引用计数管理对象。释放由编译器安排，单次释放和整组释放都是 O(1)，运行期不需要遍历堆来找垃圾，也不会有收集器停顿。

### 并发是结构化的

任务在 `region` 里产生，也必须在 `region` 结束前收齐。任务、作业、通道和取消都有统一的调度契约；跨任务的数据交换走显式移交，不靠隐藏的共享内存。

```will
func Compute(x: i32) -> i32 {
    return x + 1
}

func main() -> i32 {
    var total: i32 = 0
    region {
        var a: Task<i32> = spawn Compute(20)
        var b: Task<i32> = spawn Compute(21)
        total = a.Join()! + b.Join()!
    }
    return total
}
```

### 失败是值

可预期的失败用 `Option`、`Result`、`?` 和 `match` 表达；崩溃只表示程序缺陷，并给出可复现的报告。没有异常控制流，也没有把错误悄悄咽掉的隐式路径。

### C 是一等公民

C 函数和 will 函数使用同一套调用方式。头文件由编译器读取，类型与布局进入编译期检查；能阻塞的 C 调用必须显式标注，并交给独立的阻塞池。

### 工具能看到编译器看到的东西

will 的输出是给机器和人都能读的：诊断、索引、语义中间格式和构建决策都可以直接读取、diff 和检查。同一份输入产生同一份输出，检查和构建可以独立运行、独立缓存。

## 一眼看懂 will

```will
func main() -> i32 {
    var values: Array<i32> = new Array<i32>(4)
    for i in Range(0, 4) {
        values.Push(i * i)
    }

    var total: i32 = 0
    for v in values {
        total += v
    }

    print("sum = ", total, "\n")
    return 0
}
```

will 的语法保持小而一致：

- 一种自然的写法，不靠宏或隐式规则扩展语言；
- 值、借用和移动都在类型层显式可见；
- `if`、`match`、`for` 和块都有统一的值规则；
- 泛型、接口、错误传播和 C 互操作使用同一套名字解析与类型检查。

## 快速开始

需要 Go 1.25 或更新版本、一个 C11 工具链，以及 LLVM `clang`。

```sh
git clone https://github.com/dingtongbin/will.git
cd will
go build -o bin/will ./cmd/will

export WILL_RUNTIME="$PWD/runtime"
export WILL_CLANG="$(command -v clang)"

./bin/will run tests/build/task
```

Windows PowerShell:

```powershell
$env:WILL_RUNTIME = "$PWD\runtime"
$env:WILL_CLANG = (Get-Command clang).Source

.\bin\will.exe run tests\build\task
```

常用命令：

```sh
will check <package>       # 类型检查
will fmt <file>            # 格式化
will emit-willc <file>     # 输出语义中间格式
will emit-llvm <package>   # 输出 LLVM IR
will build <package>       # 构建可执行文件
will run <package>         # 构建并运行
will test <package>        # 运行测试函数
```

## 项目结构

```text
cmd/will/       编译器与工具的命令行入口
internal/       词法、语法、类型检查、分析与 LLVM 下降
runtime/        C11 运行时、调度器、平台层与内存管理
std/            will 标准库
tests/          语言、编译器、运行时与标准库语料
tools/          门禁、检查器与索引工具
```

## 现在能做什么

当前实现已经覆盖：

- 包与模块、导入、泛型、结构体、枚举与接口；
- `Option` / `Result`、错误传播、`defer`、模式匹配；
- 引用计数对象、数组、映射、集合与字符串；
- 任务、通道、`region` 和取消语义的基础设施；
- C 头文件解析、C ABI 调用与构建缓存；
- 诊断、索引、LSP、格式化和语义中间格式。

正在推进的部分包括完整的并行作业调度、更多挂起点上的状态机下降、标准库覆盖和运行时性能。语言与工具的行为会继续变化，直到第一个稳定版本发布。

## 许可

Apache License 2.0。见 [LICENSE](LICENSE) 与 [NOTICE](NOTICE)。
