.PHONY: go.work
go.work:
	rm go.work*
	go work init
	go work use ./gotty
	go work use ./
	go work sync
