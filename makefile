PLUGIN_BINARY=my_plugin.exe
export GO111MODULE=on
export GOOS=windows

ifeq ($(OS),Windows_NT)
	RMCMD = del /f 
else
	RMCMD = rm -f
endif

default: compile

.PHONY: clean
clean:
	${RMCMD} ${PLUGIN_BINARY}
	vagrant destroy -f

compile:
	go build -o ${PLUGIN_BINARY} .

start:
	vagrant up

setup: compile start
	vagrant provision

test: setup
	vagrant winrm -s cmd -c 'chdir C:\vagrant && go test ./plugin_tests/ -count=1 -v'