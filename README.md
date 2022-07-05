# go-ignore-cov

This project is a simple post-processor for golang coverage output file. It is paired with instructions added as comments in the source code. The objective is to be able to flag part of the code to be ignored by the code coverage and remove these parts from the coverage output file.

## Why

I can already hear some of you saying, "why would I use this? Code coverage is overated anyway...", and you would not be wrong. I've seen multiple projects trying to enforce code coverage threshold and the result was not that great. Having tests just for the sake of passing the threshold. But this is not the intended purpose of go-ignore-cov.

The purpose of this project is to make code coverage explicit. In a traditional setup with for example, 80% coverage, a developer will do some test and pass the 80% bar. Then a team mate will review the PR and won't be able to easily tell which parts are tested and which are not, unless they explicitly check a coverage report. But then again, the coverage report is about the whole codebase and not the file touched by the current pull or merge request.

To give more visibility to tested vs un-tested parts, the trick I have been using is to enforce 100% code coverage, and exclude the part that we don't want to test, but calling them out explicitly. When I review a pull or merge request and I see an instruction in the code calling out if this part of the code is tested or not, then I can reply on this specific decision and maybe provide my opinion if we should or not test this part. The important part is that it's now explicit and we can debate if it make sense or not for this part to be tested.

Using `go-ignore-cov`, you can flag the part of your code that you want to ignore from the coverage, and still enforce that 100% on everything else.

### Existing code base

What if you want to start using this on a existing codebase, how can you enforce 100% coverage? It's actually very easy. You can just add the `//coverage:ignore file` instruction at the top of every file in your project, and boom, you're at 100% coverage. Then you slowly start removing the `file` instructions and replacing them with `//coverage:ignore` statements on specific code blocks instead, still maintaining 100% coverage.

## Installation

```
go install github.com/hexira/go-ignore-cov@latest
```

## Using `go-ignore-cov`

This is a CLI tool with just a few options.

Here is an example of how to run this from your module root folder:
```
# Run test and output coverage
go test -coverprofile coverage.out -covermode count -coverpkg=./... -v ./... 

# Filter coverage output from source code ignore instructions
go-ignore-cov --file coverage.out

# Display coverage
go tool cover -func=coverage.out
```

The options for the command line are:

* `--file`: the coverage input file
* `--output`: the output coverage file. If absent, the value of `--file` is used
* `--root`: the root folder of the go module project used to produce the coverage output. By default, the working directory is used
* `--verbose`: verbose output


## The source code

There is 2 instructions that you can add to your source code.

### ignoring a code block

This is the default instruction. You add a comment like this: `//coverage:ignore` and the code block is ignored. Golang coverage works by blocks of code. The coverage is calculated from the start of a block to the start of the next block. For example, in this code:

```golang
1 func Hello(name string) {
2   callout := fmt.Sprintf("Hello %s", name)
3   if name == "World" {
4     fmt.Println("Seriously?!")
5     return 
5   }
6   fmt.Println(callout)
7 }
```
there is one block starting at "{" on line 1, and ending before "{" on line 3. The next block is starting on "{" on line 3 and ending at "}" on line 5. And finally,
there is a block for the line 6.

The default ignore instruction will ignore the whole block in which it was declared, wherever the instruction is in the block, meaning than both of these example have the same outcome:

```golang
1 func Hello(name string) {
2   callout := fmt.Sprintf("Hello %s", name)
3   //coverage:ignore
4   if name == "World" {
5     fmt.Println("Seriously?!")
6     return 
7   }
8   fmt.Println(callout)
9 }
```

```golang
1 func Hello(name string) {
2   //coverage:ignore
3   callout := fmt.Sprintf("Hello %s", name)
4   if name == "World" {
5     fmt.Println("Seriously?!")
6     return 
7   }
8   fmt.Println(callout)
9 }
```

The block in which you put the ignore instruction is completely ignored.

### ignoring a whole file

You can also ignore a whole file using `//coverage:ignore file`. You can put the comment anywhere in the file, but usually the first line is best for readability.
