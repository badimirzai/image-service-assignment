# Image Service Assignment - Instructions

Create a service for storing and retrieving image data. The service should follow best practices of a web service, examples:
- Proper data validation.
- Correct HTTP status codes.
- Proper headers (like Content-Type, etc.).

Feel free to use whatever database backend you like. For programming language we would like one of the following to be used, in order of preference:
- Go
- Python
- JS or TypeScript

The service should be performant and ideally easily scalable. We expect the code to be representative of something you'd want to merge to main in a production project. To limit the scope of the work it's OK to make less than ideal choices as long as they are motivated. For instance "I used the Windows Registry on my computer as database, it won't scale beyond three entries but if I wanted to make it scalable I would change to <insert buzzword here>".

We want the code with a nice commit history as a git bundle (`git bundle create <bundle.name> --a11`) and if you can deploy it somewhere that would be great but it's not a requirement.

## AI Tools

You're welcome to use agentic coding tools (Claude Code, Cursor, Copilot, etc.). If you do, include the full session transcript as `ai-transcript.txt` in the repo.

## Endpoints

- **`GET /v1/images`**
  List metadata for stored images.

- **`GET /v1/images/<id>`**
  Get metadata for image with id `<id>`.

- **`GET /v1/images/<id>/data`**
  Get image data for image with id `<id>`.
  *Optional GET parameter:* `?bbox=<x>,<y>,<w>,<h>` to get a cutout of the image.

- **`POST /v1/images`**
  Upload new image. Request body should be image data.

- **`PUT /v1/images/<id>`**
  Update image. Request body should be image data.

- **`POST /v1/images/batch`**
  Upload multiple images in a single request (e.g. as `multipart/form-data`). The service should process the images concurrently with bounded parallelism. Return metadata for all successfully uploaded images, and errors for any that failed. If the client disconnects mid-request, in-flight processing should be cancelled.

## Image Metadata

Image metadata should include the following fields:
- Filesize of the image.
- Image dimensions.
- Image type (gif, jpg, etc.).
- Date/time image was uploaded.
- Extra fields of your choice.

It is up to you how you want to represent the image metadata in your API.