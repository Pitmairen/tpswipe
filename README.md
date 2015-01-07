#TpSwipe

This is a simple application that reads events form the touchpad
and executes user defined commands when a gesture is detected.

Currently only external commands can be executed. The application can not 
create keyboard or mouse events on its own. This may change in the future.

It can detect swipes up,right,down,left with 2-5 fingers and pinch and spread whith 2-5 fingers.

It has been developed and tested on a Macbook air 11.6" 2013 running 
Archlinux. I don't know if it will work on other hardware.


## Install

You will need to install **go** to use this application.
After [installing](https://golang.org/doc/install) go and
setting up the working environment you can install the application
with the following commands

```
go get github.com/Pitmairen/tpswipe
go install github.com/Pitmairen/tpswipe
```

Then run the command *tpswipe*


## Config

Create a config file on ~/.config/tpswipe.conf or specify a config file with the -config argument.

The file must contain the path to the /dev/input[X] device that represents the touchpad. Se the 
example config file for an example configuration.

There is also a -test which can be used to test if the gestures are detected. 



